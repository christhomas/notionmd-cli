package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/pflag"

	"github.com/brittonhayes/notionmd"
	"github.com/dstotijn/go-notion"
)

// rewriteContent applies rewrite-text mapping from a file to the markdown content.
func rewriteContent(mdContent []byte, mdPath, rewriteLink string) ([]byte, error) {
	data, err := os.ReadFile(rewriteLink)
	if err != nil {
		return nil, fmt.Errorf("Error reading rewrite-link mapping file: %w", err)
	}

	var singlePage map[string]string
	if err := json.Unmarshal(data, &singlePage); err == nil {
		debugLog("[DEBUG] Detected single-page rewrite mapping with %d links\n", len(singlePage))
		return []byte(rewriteTextMap(string(mdContent), singlePage)), nil
	}

	var multiPage map[string]map[string]string
	if err := json.Unmarshal(data, &multiPage); err == nil {
		debugLog("[DEBUG] Detected multi-page rewrite mapping. Searching for a matching page key in: %s\n", mdPath)
		var (
			matchedKey string
			pageMap    map[string]string
		)
		for key, candidate := range multiPage {
			if strings.Contains(mdPath, key) {
				matchedKey = key
				pageMap = candidate
				break
			}
		}
		if matchedKey != "" {
			debugLog("[DEBUG] Found %d links for page key '%s' (matched in: %s)\n", len(pageMap), matchedKey, mdPath)
			return []byte(rewriteTextMap(string(mdContent), pageMap)), nil
		}
		debugLog("[DEBUG] No mapping found for any key in '%s'. No rewrite applied.\n", mdPath)
		return mdContent, nil // no mapping for this page, return original content
	}
	debugLog("[DEBUG] Could not decode rewrite-link mapping file as single or multi-page mapping")
	return nil, fmt.Errorf("Error decoding rewrite-link mapping file as single or multi-page mapping")
}

var (
	debugEnabled bool
	Version      = "dev"
)

// debugLog prints debug messages if debugEnabled is true.
func debugLog(format string, args ...interface{}) {
	if debugEnabled {
		fmt.Printf(format, args...)
	}
}

// rewriteTextMap replaces markdown links according to the mapping
func rewriteTextMap(content string, linkMap map[string]string) string {
	fmt.Printf("Rewriting %d links:\n", len(linkMap))
	for old, new := range linkMap {
		fmt.Printf("Replacing:  '%s' -> '%s'\n", old, new)
		// Replace text if present anywhere
		content = strings.ReplaceAll(content, old, new)
	}
	return content
}

func main() {
	var (
		token        string
		pageID       string
		mdPath       string
		appendF      bool
		replaceF     bool
		useHash      bool
		hashProperty string
		rewriteText  string
		dryRun       bool
		debugFlag    bool
		version      bool
	)
	pflag.StringVar(&token, "token", "", "Notion integration token")
	pflag.StringVar(&pageID, "page", "", "Target Notion page ID")
	pflag.StringVar(&mdPath, "md", "", "Path to markdown file")
	pflag.BoolVar(&appendF, "append", false, "Append content to the bottom of the existing page (default)")
	pflag.BoolVar(&replaceF, "replace", false, "Replace all existing content with new content")
	pflag.BoolVar(&useHash, "use-hash", false, "Store and check content hash in a dedicated metadata block and/or property.")
	pflag.StringVar(&hashProperty, "hash-property", "", "Optionally specify property name for content hash, e.g. --hash-property=MyPropName")
	pflag.StringVar(&rewriteText, "rewrite-text", "", "Path to JSON file mapping links to rewrite in the markdown file")
	pflag.BoolVar(&dryRun, "dry-run", false, "Run all logic except Notion sync (no changes will be made to Notion)")
	pflag.BoolVar(&debugFlag, "debug", false, "Enable debug output")
	pflag.BoolVarP(&version, "version", "v", false, "Print version and exit")
	pflag.Parse()

	if version {
		fmt.Println(Version)
		os.Exit(0)
	}

	debugEnabled = debugFlag

	debugLog("Given: \n--token '%s' \n--page '%s' \n--md '%s' \n--append '%t' \n--replace '%t' \n--use-hash '%t' \n--hash-property '%s' \n--rewrite-link '%s'\n", token, pageID, mdPath, appendF, replaceF, useHash, hashProperty, rewriteText)

	if token == "" || pageID == "" || mdPath == "" || len(os.Args) == 1 {
		pflag.Usage()
		os.Exit(1)
	}

	if appendF && replaceF {
		fmt.Println("Cannot use both --append and --replace flags at the same time.")
		os.Exit(1)
	}

	mdContent, err := os.ReadFile(mdPath)
	if err != nil {
		fmt.Println("Error reading markdown file:", err)
		os.Exit(1)
	}

	// Rewrite links if mapping is provided
	if rewriteText != "" {
		mapped, err := rewriteContent(mdContent, mdPath, rewriteText)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		mdContent = mapped
	}

	contentBlocks, err := notionmd.Convert(string(mdContent))
	if err != nil {
		fmt.Println("Markdown conversion failed:", err)
		os.Exit(1)
	}

	// --- content_hash optimization ---
	// Compute hash of the input markdown file
	hashBytes := sha256.Sum256(mdContent)
	contentHash := fmt.Sprintf("%x", hashBytes[:])

	client := notion.NewClient(token)
	ctx := context.Background()

	if dryRun {
		fmt.Println("[DRY RUN] All parsing, conversion, and hash logic completed. No changes made to Notion.")
		return
	}

	if useHash {
		contentHashPropertyName := "Content Hash"
		if hashProperty != "" {
			contentHashPropertyName = hashProperty
		}
		propertyHash, err := getProperty(client, ctx, pageID, contentHashPropertyName)
		if err != nil {
			fmt.Printf("Error getting '%s' property: %s\n", contentHashPropertyName, err)
			os.Exit(1)
		}
		fmt.Printf("Page hash (Property Name: '%s'): %s\n", contentHashPropertyName, propertyHash)
		fmt.Printf("Content hash: %s\n", contentHash)
		if propertyHash == contentHash {
			fmt.Println("No content change detected. Skipping update.")
			os.Exit(0)
		}
		if err := setProperty(client, ctx, pageID, contentHashPropertyName, contentHash); err != nil {
			fmt.Printf("Warning: failed to set '%s' property: %s\n", contentHashPropertyName, err)
		}
	}
	// --- end content_hash optimization ---

	if replaceF {
		if err := clearPageContent(token, pageID); err != nil {
			fmt.Printf("Error clearing Notion page: %s\n", err)
			os.Exit(1)
		}
	}

	if err := replacePageContent(token, pageID, contentBlocks); err != nil {
		fmt.Printf("Error updating Notion page: %s\n", err)
		os.Exit(1)
	}

	fmt.Println("✅ Page updated successfully.")
}

// getAllPageBlocks retrieves all blocks from a Notion page (recursively, if needed)
func getAllPageBlocks(client *notion.Client, ctx context.Context, pageID string) ([]notion.Block, error) {
	var blocks []notion.Block
	startCursor := ""
	for {
		resp, err := client.FindBlockChildrenByID(ctx, pageID, &notion.PaginationQuery{StartCursor: startCursor})
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, resp.Results...)
		if !resp.HasMore || resp.NextCursor == nil || *resp.NextCursor == "" {
			break
		}
		startCursor = *resp.NextCursor
	}
	return blocks, nil
}

// filterContentHashBlock takes a list of blocks, finds the content hash block,
// removes it from the list, and returns the new list and the found hash (or "" if not present).
func filterContentHashBlock(blocks []notion.Block) ([]notion.Block, string) {
	var foundHash string
	var filtered []notion.Block
	for _, block := range blocks {
		if code, ok := block.(notion.CodeBlock); ok {
			for _, rt := range code.RichText {
				text := strings.TrimSpace(rt.PlainText)
				var meta PageMetadata
				if err := json.Unmarshal([]byte(text), &meta); err == nil && meta.ContentHash != "" {
					foundHash = meta.ContentHash
					goto skip
				}
			}
		}
		filtered = append(filtered, block)
	skip:
	}
	return filtered, foundHash
}

// makeContentHashTableBlock creates a table block for the hash metadata
func makeContentHashTableBlock(hash string) notion.Block {
	return notion.TableBlock{
		TableWidth:      2,
		HasColumnHeader: false,
		HasRowHeader:    false,
		Children: []notion.Block{
			notion.TableRowBlock{
				Cells: [][]notion.RichText{
					{{Text: &notion.Text{Content: "Automation Metadata"}}},
					{{Text: &notion.Text{Content: ""}}},
				},
			},
			notion.TableRowBlock{
				Cells: [][]notion.RichText{
					{{Text: &notion.Text{Content: "content_hash"}}},
					{{Text: &notion.Text{Content: hash}}},
				},
			},
		},
	}
}

// Helpers to get and set a rich_text property on the Notion page (go-notion v0.11.0)
func getProperty(client *notion.Client, ctx context.Context, pageID, propName string) (string, error) {
	page, err := client.FindPageByID(ctx, pageID)
	if err != nil {
		return "", err
	}
	props, ok := page.Properties.(notion.DatabasePageProperties)
	if !ok {
		return "", nil // not a database page or unexpected type
	}
	prop, ok := props[propName]
	if !ok {
		return "", nil // property not set
	}
	if len(prop.RichText) > 0 {
		return prop.RichText[0].PlainText, nil
	}
	return "", nil
}

func setProperty(client *notion.Client, ctx context.Context, pageID, propName, value string) error {
	_, err := client.UpdatePage(ctx, pageID, notion.UpdatePageParams{
		DatabasePageProperties: notion.DatabasePageProperties{
			propName: notion.DatabasePageProperty{
				Type:     "rich_text",
				RichText: []notion.RichText{{Text: &notion.Text{Content: value}}},
			},
		},
	})
	return err
}

// PageMetadata is the metadata stored in the code block
type PageMetadata struct {
	ContentHash string `json:"content_hash"`
}

// makeContentHashCodeBlock creates a new code block for the hash as a JSON object
func makeContentHashCodeBlock(hash string) notion.Block {
	lang := "plain text"
	return notion.CodeBlock{
		RichText: []notion.RichText{{
			Text: &notion.Text{Content: hash},
		}},
		Language: &lang,
		Caption: []notion.RichText{{
			Text: &notion.Text{Content: "Automation Metadata"},
		}},
	}
}

// clearPageContent deletes all child blocks of the given page using go-notion.
func clearPageContent(token, pageID string) error {
	client := notion.NewClient(token)
	ctx := context.Background()
	startCursor := ""
	for {
		resp, err := client.FindBlockChildrenByID(ctx, pageID, &notion.PaginationQuery{StartCursor: startCursor})
		if err != nil {
			return fmt.Errorf("failed to fetch children: %w", err)
		}
		for _, block := range resp.Results {
			_, err := client.DeleteBlock(ctx, block.ID())
			if err != nil {
				return fmt.Errorf("failed to delete block %s: %w", block.ID(), err)
			}
		}
		if !resp.HasMore || resp.NextCursor == nil || *resp.NextCursor == "" {
			break
		}
		startCursor = *resp.NextCursor
	}
	return nil
}

func replacePageContent(token, pageID string, blocks []notion.Block) error {
	url := fmt.Sprintf("https://api.notion.com/v1/blocks/%s/children", pageID)

	body := map[string]interface{}{
		"children": blocks,
	}
	jsonData, _ := json.Marshal(body)

	req, _ := http.NewRequest("PATCH", url, bytes.NewReader(jsonData))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Notion-Version", "2022-06-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Notion API error %d: %s", resp.StatusCode, string(b))
	}

	return nil
}
