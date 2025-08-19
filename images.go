package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/dstotijn/go-notion"
)

// Regular expression to find Markdown image references: ![alt text](path/to/image.jpg)
var markdownImageRegex = regexp.MustCompile(`!\[([^\]]*)\]\(([^)]+)\)`)

// Regular expression to find HTML img tags: <img src="path/to/image.jpg" alt="alt text" width="500" height="300">
var htmlImageRegex = regexp.MustCompile(`<img[^>]*\ssrc=["']([^"']+)["'][^>]*(?:\salt=["']([^"']*)["'][^>]*)?(?:\swidth=["']([0-9]+)["'][^>]*)?(?:\sheight=["']([0-9]+)["'][^>]*)?[^>]*/?>`)

// ImageReference represents an image reference in a Markdown document
type ImageReference struct {
	AltText string
	Path    string
	IsLocal bool
	Width   int // Optional width from URL parameters
	Height  int // Optional height from URL parameters
}

type FileUpload struct {
	ID string `json:"id"`
}

type ImageBlock struct {
	notion.ImageBlock
	FileUpload FileUpload `json:"file_upload,omitempty"`
}

func (b ImageBlock) MarshalJSON() ([]byte, error) {
	base, err := structToMap(b.ImageBlock)
	if err != nil {
		return nil, err
	}
	if img, ok := base["image"].(map[string]interface{}); ok {
		img["file_upload"] = b.FileUpload
	}
	return json.Marshal(base)
}

func structToMap(v interface{}) (map[string]interface{}, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// FindImageReferences scans content for image references (both Markdown and HTML)
func FindImageReferences(content string) []ImageReference {
	refs := make([]ImageReference, 0)

	// Find Markdown image references
	mdMatches := markdownImageRegex.FindAllStringSubmatch(content, -1)
	for _, match := range mdMatches {
		if len(match) >= 3 {
			altText := match[1]
			origPath := match[2]

			// Parse the path to extract any width/height parameters
			path, width, height := parseImagePath(origPath)
			isLocal := !strings.HasPrefix(path, "http://") && !strings.HasPrefix(path, "https://")

			refs = append(refs, ImageReference{
				AltText: altText,
				Path:    path,
				IsLocal: isLocal,
				Width:   width,
				Height:  height,
			})
		}
	}

	// Find HTML img tags
	htmlMatches := htmlImageRegex.FindAllStringSubmatch(content, -1)
	for _, match := range htmlMatches {
		if len(match) >= 2 { // At minimum we need the src attribute
			src := match[1]
			altText := ""
			width, height := 0, 0

			// Extract alt text if available (index 2)
			if len(match) >= 3 && match[2] != "" {
				altText = match[2]
			}

			// Extract width if available (index 3)
			if len(match) >= 4 && match[3] != "" {
				if w, err := strconv.Atoi(match[3]); err == nil {
					width = w
				}
			}

			// Extract height if available (index 4)
			if len(match) >= 5 && match[4] != "" {
				if h, err := strconv.Atoi(match[4]); err == nil {
					height = h
				}
			}

			// Check for URL parameters in src that might also specify dimensions
			// This allows for both <img src="image.jpg?width=500&height=300"> and <img src="image.jpg" width="500" height="300">
			srcPath, srcWidth, srcHeight := parseImagePath(src)

			// Use explicit width/height attributes if available, otherwise use URL parameters
			if width == 0 {
				width = srcWidth
			}
			if height == 0 {
				height = srcHeight
			}

			isLocal := !strings.HasPrefix(srcPath, "http://") && !strings.HasPrefix(srcPath, "https://")

			refs = append(refs, ImageReference{
				AltText: altText,
				Path:    srcPath,
				IsLocal: isLocal,
				Width:   width,
				Height:  height,
			})
		}
	}

	return refs
}

// parseImagePath extracts width and height parameters from image URLs
// Returns the cleaned path (without dimension parameters), width, and height
func parseImagePath(path string) (string, int, int) {
	// Check if the path contains URL parameters
	queryIndex := strings.IndexAny(path, "?")
	if queryIndex == -1 {
		return path, 0, 0 // No parameters found
	}

	// Split the path into base and query parts
	basePath := path[:queryIndex]
	queryPart := path[queryIndex+1:]

	// Parse the query parameters
	params := strings.Split(queryPart, "&")
	width, height := 0, 0
	keepParams := []string{}

	for _, param := range params {
		parts := strings.SplitN(param, "=", 2)
		if len(parts) != 2 {
			keepParams = append(keepParams, param)
			continue
		}

		key, value := parts[0], parts[1]
		switch key {
		case "width":
			if w, err := strconv.Atoi(value); err == nil {
				width = w
			} else {
				keepParams = append(keepParams, param)
			}
		case "height":
			if h, err := strconv.Atoi(value); err == nil {
				height = h
			} else {
				keepParams = append(keepParams, param)
			}
		default:
			keepParams = append(keepParams, param)
		}
	}

	// Reconstruct the path without width/height parameters
	if len(keepParams) > 0 {
		basePath += "?" + strings.Join(keepParams, "&")
	}

	return basePath, width, height
}

// ProcessImageBlocks processes Notion blocks and replaces image references with actual image blocks
// basePath is the path to the markdown file, used to resolve relative image paths
func ProcessImageBlocks(blocks []notion.Block, basePath string, notionClient NotionClientInterface) ([]notion.Block, error) {
	result := make([]notion.Block, 0, len(blocks))

	for _, block := range blocks {
		// Check if this is a paragraph block that might contain an image reference
		if paragraphBlock, ok := block.(*notion.ParagraphBlock); ok && paragraphBlock != nil {
			// Process paragraph blocks that might contain image references
			processedBlocks, replaced, err := processImageInParagraph(paragraphBlock, basePath, notionClient)
			if err != nil {
				return nil, err
			}

			// Add the processed blocks
			result = append(result, processedBlocks...)

			// If the paragraph was replaced with an image block, continue to the next block
			if replaced {
				continue
			}
		} else {
			// For all other block types, add them as-is
			result = append(result, block)
		}
	}

	return result, nil
}

// processImageInParagraph checks if a paragraph block contains an image reference and processes it
// Returns the processed blocks, a boolean indicating if the paragraph was replaced, and any error
func processImageInParagraph(paragraphBlock *notion.ParagraphBlock, basePath string, notionClient NotionClientInterface) ([]notion.Block, bool, error) {
	// Extract text content from the paragraph
	var fullText string
	for _, richText := range paragraphBlock.RichText {
		if richText.Text != nil {
			fullText += richText.Text.Content
		}
	}

	// Check if the paragraph contains an image reference
	imageRefs := FindImageReferences(fullText)
	if len(imageRefs) == 0 {
		// No image references found, return the original paragraph
		return []notion.Block{paragraphBlock}, false, nil
	}

	// Process the first image reference (typically there should only be one per paragraph)
	ref := imageRefs[0]

	// Create the appropriate image block
	var imageBlock notion.Block

	if ref.IsLocal {
		// Process local image
		imagePath := ref.Path
		if !filepath.IsAbs(imagePath) {
			imagePath = filepath.Join(filepath.Dir(basePath), imagePath)
		}

		// Check if file exists
		if _, err := os.Stat(imagePath); os.IsNotExist(err) {
			return nil, false, fmt.Errorf("local image file not found: %s", imagePath)
		}

		// Create image block from local file with dimensions
		fileUploadID, err := notionClient.UploadFile(imagePath)
		if err != nil {
			return nil, false, err
		}
		imageBlock = createImageBlockWithFileUpload(fileUploadID, ref.AltText, ref.Width, ref.Height)
	} else {
		// Process external image URL with dimensions
		imageBlock = createImageBlockFromURL(ref.Path, ref.AltText, ref.Width, ref.Height)
	}

	// Return the image block, indicating the paragraph was replaced
	return []notion.Block{imageBlock}, true, nil
}

// createImageBlockWithFileUpload creates a Notion image block using a file upload ID
func createImageBlockWithFileUpload(fileUploadID string, altText string, width, height int) ImageBlock {
	// Create caption text
	caption := []notion.RichText{}

	// Add alt text to caption if provided
	if altText != "" {
		caption = append(caption, notion.RichText{
			Type: notion.RichTextTypeText,
			Text: &notion.Text{
				Content: altText,
			},
		})
	}

	// Add width/height information to the caption if provided
	if width > 0 || height > 0 {
		dimensionInfo := " ("
		if width > 0 {
			dimensionInfo += fmt.Sprintf("width: %dpx", width)
		}
		if width > 0 && height > 0 {
			dimensionInfo += ", "
		}
		if height > 0 {
			dimensionInfo += fmt.Sprintf("height: %dpx", height)
		}
		dimensionInfo += ")"

		// Add dimension info to caption
		caption = append(caption, notion.RichText{
			Type: notion.RichTextTypeText,
			Text: &notion.Text{
				Content: dimensionInfo,
			},
		})
	}

	// Create an image block referencing the uploaded file (Notion API expects type: "file" and a file object with id)
	return ImageBlock{
		ImageBlock: notion.ImageBlock{
			Type:    "file_upload",
			Caption: caption,
		},
		FileUpload: FileUpload{
			ID: fileUploadID,
		},
	}
}

// createImageBlockFromURL creates a Notion image block from a URL
func createImageBlockFromURL(url string, altText string, width, height int) notion.Block {
	// Create image block with caption
	caption := []notion.RichText{}
	if altText != "" {
		caption = append(caption, notion.RichText{
			Type: notion.RichTextTypeText,
			Text: &notion.Text{
				Content: altText,
			},
		})
	}

	// Create the image block with external URL
	imageBlock := &notion.ImageBlock{
		Caption: caption,
	}

	// Set the external URL
	imageBlock.External = &notion.FileExternal{
		URL: url,
	}

	// Add width/height information to the caption if provided
	if width > 0 || height > 0 {
		dimensionInfo := " ("
		if width > 0 {
			dimensionInfo += fmt.Sprintf("width: %dpx", width)
		}
		if width > 0 && height > 0 {
			dimensionInfo += ", "
		}
		if height > 0 {
			dimensionInfo += fmt.Sprintf("height: %dpx", height)
		}
		dimensionInfo += ")"

		// Add dimension info to caption
		imageBlock.Caption = append(imageBlock.Caption, notion.RichText{
			Type: notion.RichTextTypeText,
			Text: &notion.Text{
				Content: dimensionInfo,
			},
		})
	}

	return imageBlock
}
