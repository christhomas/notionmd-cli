package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"

	"github.com/dstotijn/go-notion"
)

type NotionClientInterface interface {
	UploadFile(filePath string) (fileID string, err error)
	AddPageContent(pageID string, blocks []notion.Block) error
	ClearPageContent(pageID string) error
	UpdatePageTitle(pageID string, titleBlock notion.Block) error
	GetProperty(pageID, propName string) (string, error)
	SetProperty(pageID, propName, value string) error
}

type NotionClient struct {
	NotionToken  string
	NotionClient *notion.Client
	NotionHTTP   *NotionHTTP
}

type fileUploadResponse struct {
	ID        string `json:"id"`
	UploadURL string `json:"upload_url"`
}

func NewNotionClient(token string) *NotionClient {
	return &NotionClient{
		NotionToken:  token,
		NotionClient: notion.NewClient(token),
		NotionHTTP:   NewNotionHTTP(token, "2022-06-28"),
	}
}

func (c *NotionClient) UploadFile(filePath string) (string, error) {
	filename := filepath.Base(filePath)

	uploadResp, err := c.createFileUploadObject()
	if err != nil {
		return "", fmt.Errorf("failed to create file upload object: %w", err)
	}

	err = c.uploadFileContent(uploadResp.UploadURL, filePath, filename)
	if err != nil {
		return "", fmt.Errorf("failed to upload file content: %w", err)
	}

	return uploadResp.ID, nil
}

func getFileContentType(filePath string) string {
	ext := filepath.Ext(filePath)
	mimeType := mime.TypeByExtension(ext)
	if mimeType != "" {
		return mimeType
	}
	// Fallback: try to sniff the content
	file, err := os.Open(filePath)
	if err != nil {
		return "application/octet-stream"
	}
	defer file.Close()
	buf := make([]byte, 512)
	n, _ := file.Read(buf)
	return http.DetectContentType(buf[:n])
}

func (c *NotionClient) createFileUploadObject() (*fileUploadResponse, error) {
	emptyBody := []byte("{}")
	resp, err := c.NotionHTTP.Post("https://api.notion.com/v1/file_uploads", emptyBody, "application/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Notion API error %d: %s", resp.StatusCode, string(bodyBytes))
	}
	var uploadResp fileUploadResponse
	if err := json.NewDecoder(resp.Body).Decode(&uploadResp); err != nil {
		return nil, err
	}
	return &uploadResp, nil
}

func (c *NotionClient) uploadFileContent(uploadURL, filePath, filename string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()
	var requestBodyBuf bytes.Buffer
	writer := multipart.NewWriter(&requestBodyBuf)
	contentType := getFileContentType(filePath)
	headers := make(textproto.MIMEHeader)
	headers.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, filename))
	headers.Set("Content-Type", contentType)
	part, err := writer.CreatePart(headers)
	if err != nil {
		return err
	}
	_, err = io.Copy(part, file)
	if err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	fmt.Printf("Uploading file %s to %s\n", filePath, uploadURL)
	resp, err := c.NotionHTTP.Post(uploadURL, requestBodyBuf.Bytes(), writer.FormDataContentType())
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upload error %d: %s", resp.StatusCode, string(bodyBytes))
	}
	fmt.Printf("File %s uploaded successfully\n", filePath)
	return nil
}

// AddPageContent adds blocks to a Notion page
func (c *NotionClient) AddPageContent(pageID string, blocks []notion.Block) error {
	url := fmt.Sprintf("https://api.notion.com/v1/blocks/%s/children", pageID)
	body := map[string]interface{}{
		"children": blocks,
	}
	jsonData, _ := json.Marshal(body)

	resp, err := c.NotionHTTP.Patch(url, jsonData, "application/json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		j, e := json.Marshal(body)
		if e != nil {
			fmt.Printf("Unable to marshal body: %v\n", e)
		}
		fmt.Printf("Body: %s\n", j)
		return fmt.Errorf("Notion API error %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

// ClearPageContent deletes all child blocks of the given page
func (c *NotionClient) ClearPageContent(pageID string) error {
	ctx := context.Background()
	startCursor := ""
	for {
		resp, err := c.NotionClient.FindBlockChildrenByID(ctx, pageID, &notion.PaginationQuery{StartCursor: startCursor})
		if err != nil {
			return fmt.Errorf("failed to fetch children: %w", err)
		}
		for _, block := range resp.Results {
			_, err := c.NotionClient.DeleteBlock(ctx, block.ID())
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

// UpdatePageTitle updates the Notion page's title using a Heading1Block
func (c *NotionClient) UpdatePageTitle(pageID string, titleBlock notion.Block) error {
	ctx := context.Background()
	heading, ok := titleBlock.(notion.Heading1Block)
	if !ok {
		return fmt.Errorf("titleBlock is not a Heading1Block")
	}
	if len(heading.RichText) == 0 {
		return fmt.Errorf("Heading1Block has no rich text")
	}
	title := heading.RichText[0].PlainText
	page, err := c.NotionClient.FindPageByID(ctx, pageID)
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
	_, err = c.NotionClient.UpdatePage(ctx, pageID, notion.UpdatePageParams{
		DatabasePageProperties: notion.DatabasePageProperties{
			titleProp: notion.DatabasePageProperty{
				Type:  "title",
				Title: []notion.RichText{{Text: &notion.Text{Content: title}}},
			},
		},
	})
	if err != nil {
		return err
	}
	return nil
}

// GetProperty gets a rich_text property on the Notion page
func (c *NotionClient) GetProperty(pageID, propName string) (string, error) {
	ctx := context.Background()
	page, err := c.NotionClient.FindPageByID(ctx, pageID)
	if err != nil {
		return "", err
	}
	props, ok := page.Properties.(notion.DatabasePageProperties)
	if !ok {
		return "", nil
	}
	prop, ok := props[propName]
	if !ok {
		return "", nil
	}
	if len(prop.RichText) > 0 {
		return prop.RichText[0].PlainText, nil
	}
	return "", nil
}

// SetProperty sets a rich_text property on the Notion page
func (c *NotionClient) SetProperty(pageID, propName, value string) error {
	ctx := context.Background()
	_, err := c.NotionClient.UpdatePage(ctx, pageID, notion.UpdatePageParams{
		DatabasePageProperties: notion.DatabasePageProperties{
			propName: notion.DatabasePageProperty{
				Type:     "rich_text",
				RichText: []notion.RichText{{Text: &notion.Text{Content: value}}},
			},
		},
	})
	return err
}
