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

var (
	debugEnabled bool
	Version      = "dev"
)

// PageMetadata is the metadata stored in the code block
type PageMetadata struct {
	ContentHash string `json:"content_hash"`
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

	debugLog("Given: \n--token '%s' \n--page '%s' \n--md '%s' \n--append '%t' \n--replace '%t' \n--use-hash '%t' \n--hash-property '%s' \n--rewrite-text '%s'\n", token, pageID, mdPath, appendF, replaceF, useHash, hashProperty, rewriteText)

	if token == "" || pageID == "" || mdPath == "" || len(os.Args) == 1 {
		pflag.Usage()
		os.Exit(1)
	}

	if appendF && replaceF {
		fmt.Println("Cannot use both --append and --replace flags at the same time.")
		os.Exit(1)
	}

	printTitle(mdPath, replaceF, useHash, rewriteText)

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

	// Debug all block types
	debugLog("Found %d blocks after markdown conversion\n", len(contentBlocks))
	for i, block := range contentBlocks {
		debugLog("Block %d is of type: %T\n", i, block)

		// Check specifically for code blocks
		if codeBlock, ok := block.(*notion.CodeBlock); ok {
			debugLog("Found code block at index %d\n", i)
			if codeBlock.Language == nil {
				debugLog("  Language is nil\n")
			} else {
				debugLog("  Language is '%s'\n", *codeBlock.Language)
			}
		}
	}

	// --- content_hash optimization ---
	// Compute hash of the input markdown file
	hashBytes := sha256.Sum256(mdContent)
	contentHash := fmt.Sprintf("%x", hashBytes[:])

	client := notion.NewClient(token)
	ctx := context.Background()

	// Validate and patch Notion blocks before sending to API
	var titleBlock notion.Block
	contentBlocks = validateContentBlocks(contentBlocks)
	titleBlock, contentBlocks = filterTitleBlock(contentBlocks)
	if titleBlock != nil {
		updatePageTitle(client, ctx, pageID, titleBlock)
	}

	if dryRun {
		fmt.Println("[DRY RUN] All parsing, conversion, and hash logic completed. No changes made to Notion.")
		fmt.Printf("[MD CONTENT]\n%s\n\n", mdContent)
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
			fmt.Println("⚠️ No content change detected. Skipping update.")
			os.Exit(0)
		}
		if err := setProperty(client, ctx, pageID, contentHashPropertyName, contentHash); err != nil {
			fmt.Printf("Warning: failed to set '%s' property: %s\n", contentHashPropertyName, err)
		}
	}

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

// rewriteContent applies rewrite-text mapping from a file to the markdown content.
func rewriteContent(mdContent []byte, mdPath, rewriteLink string) ([]byte, error) {
	data, err := os.ReadFile(rewriteLink)
	if err != nil {
		return nil, fmt.Errorf("Error reading rewrite-text mapping file: %w", err)
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
	debugLog("[DEBUG] Could not decode rewrite-text mapping file as single or multi-page mapping")
	return nil, fmt.Errorf("Error decoding rewrite-text mapping file as single or multi-page mapping")
}

// debugLog prints debug messages if debugEnabled is true.
func debugLog(format string, args ...interface{}) {
	if debugEnabled {
		fmt.Printf(format, args...)
	}
}

// mapLanguageToNotionCompatible maps common language identifiers to Notion-compatible values
func mapLanguageToNotionCompatible(lang string) string {
	langMap := map[string]string{
		"sh":   "shell",
		"bash": "bash",
		"zsh":  "shell",
		"js":   "javascript",
		"ts":   "typescript",
		"py":   "python",
		"rb":   "ruby",
		"cs":   "c#",
		"cpp":  "c++",
		"yml":  "yaml",
		"text": "plain text",
		"txt":  "plain text",
	}

	if mapped, ok := langMap[lang]; ok {
		return mapped
	}
	return lang
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

// filterTitleBlock checks if the first block is a title node, removes and returns it. Otherwise returns nil, blocks.
func filterTitleBlock(blocks []notion.Block) (notion.Block, []notion.Block) {
	if len(blocks) == 0 {
		return nil, blocks
	}
	if _, ok := blocks[0].(notion.Heading1Block); ok {
		return blocks[0], blocks[1:]
	}
	return nil, blocks
}

// processCodeBlock handles both pointer and non-pointer code blocks and returns a non-pointer type
func processCodeBlock(i int, codeBlock notion.CodeBlock) notion.CodeBlock {
	defaultLang := "plain text"
	if codeBlock.Language == nil {
		codeBlock.Language = &defaultLang
		fmt.Printf("⚠️  Fixed code block at index %d: set nil language to '%s'\n", i, defaultLang)
	} else {
		// Map language to Notion-compatible value
		originalLang := *codeBlock.Language
		mappedLang := mapLanguageToNotionCompatible(originalLang)
		if mappedLang != originalLang {
			*codeBlock.Language = mappedLang
			fmt.Printf("⚠️  Fixed code block at index %d: mapped language from '%s' to '%s'\n", i, originalLang, mappedLang)
		}
	}
	fmt.Printf("⚠️  Code block at index %d has language: %s\n", i, *codeBlock.Language)
	return codeBlock
}

// validateContentBlocks scans and patches Notion blocks for known API problems (e.g., empty bulleted list items)
func validateContentBlocks(blocks []notion.Block) []notion.Block {
	var patched []notion.Block
	for i, block := range blocks {
		// Convert pointer to non-pointer type
		if _, ok := block.(*notion.CodeBlock); ok {
			block = notion.CodeBlock(*block.(*notion.CodeBlock))
		}

		switch b := block.(type) {
		case notion.BulletedListItemBlock:
			if len(b.RichText) == 0 || (len(b.RichText) == 1 && b.RichText[0].PlainText == "") {
				fmt.Printf("⚠️  Skipping empty bulleted list item at index %d\n", i)
				continue
			}
			if len(b.Children) > 0 {
				b.Children = validateContentBlocks(b.Children)
			}
			patched = append(patched, b)
		case notion.NumberedListItemBlock:
			if len(b.RichText) == 0 || (len(b.RichText) == 1 && b.RichText[0].PlainText == "") {
				fmt.Printf("⚠️  Skipping empty numbered list item at index %d\n", i)
				continue
			}
			if len(b.Children) > 0 {
				b.Children = validateContentBlocks(b.Children)
			}
			patched = append(patched, b)
		case notion.CodeBlock:
			b = processCodeBlock(i, b)
			patched = append(patched, b)
		default:
			// TODO: add more block type checks as needed
			patched = append(patched, block)
		}
	}
	return patched
}

// printTitle prints a detailed operation title based on flags and arguments
func printTitle(mdPath string, replaceF, useHash bool, rewriteText string) {
	mode := "append"
	if replaceF {
		mode = "replace"
	}
	details := []string{"NotionMD Cli: Processing file '" + mdPath + "' using " + mode}
	if useHash {
		details = append(details, "content hash check enabled")
	}
	if rewriteText != "" {
		details = append(details, "rewrite mapping: '"+rewriteText+"'")
	}
	fmt.Println("\n===== " + strings.Join(details, ", ") + " =====\n")
}

// updatePageTitle updates the Notion page's title using a Heading1Block.
func updatePageTitle(client *notion.Client, ctx context.Context, pageID string, titleBlock notion.Block) error {
	heading, ok := titleBlock.(notion.Heading1Block)
	if !ok {
		return fmt.Errorf("titleBlock is not a Heading1Block")
	}
	if len(heading.RichText) == 0 {
		return fmt.Errorf("Heading1Block has no rich text")
	}
	title := heading.RichText[0].PlainText

	// Find the correct title property name
	page, err := client.FindPageByID(ctx, pageID)
	if err != nil {
		return fmt.Errorf("failed to fetch page: %w", err)
	}
	props, ok := page.Properties.(notion.DatabasePageProperties)
	if !ok {
		return fmt.Errorf("unexpected properties type for page")
	}
	titleProp := ""
	for propName, prop := range props {
		if prop.Type == "title" {
			titleProp = propName
			break
		}
	}
	if titleProp == "" {
		return fmt.Errorf("no title property found on page")
	}

	_, err = client.UpdatePage(ctx, pageID, notion.UpdatePageParams{
		DatabasePageProperties: notion.DatabasePageProperties{
			titleProp: notion.DatabasePageProperty{
				Type:  "title",
				Title: []notion.RichText{{Text: &notion.Text{Content: title}}},
			},
		},
	})
	if err != nil {
		fmt.Printf("Error updating page title: %v\n", err)
		return err
	}
	return nil
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
