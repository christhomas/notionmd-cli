package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"context"

	"github.com/brittonhayes/notionmd"
	"github.com/dstotijn/go-notion"
)

func main() {
	var (
		token   = flag.String("token", "", "Notion integration token")
		pageID  = flag.String("page", "", "Target Notion page ID")
		mdPath  = flag.String("md", "", "Path to markdown file")
		appendF = flag.Bool("append", false, "Append content to the bottom of the existing page (default)")
		replaceF = flag.Bool("replace", false, "Replace all existing content with new content")
	)
	flag.Parse()

	if *token == "" || *pageID == "" || *mdPath == "" {
		fmt.Println("Usage: notionmd-cli -token <token> -page <page_id> -md <markdown-file> [--append|--replace]")
		os.Exit(1)
	}

	if *appendF && *replaceF {
		fmt.Println("Cannot use both --append and --replace flags at the same time.")
		os.Exit(1)
	}

	mdContent, err := os.ReadFile(*mdPath)
	if err != nil {
		fmt.Println("Error reading markdown file:", err)
		os.Exit(1)
	}

	blocks, err := notionmd.Convert(string(mdContent))
	if err != nil {
		fmt.Println("Markdown conversion failed:", err)
		os.Exit(1)
	}

	if *replaceF {
		if err := clearPageContent(*token, *pageID); err != nil {
			fmt.Println("Error clearing Notion page:", err)
			os.Exit(1)
		}
	}

	if err := replacePageContent(*token, *pageID, blocks); err != nil {
		fmt.Println("Error updating Notion page:", err)
		os.Exit(1)
	}

	fmt.Println("✅ Page updated successfully.")
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
