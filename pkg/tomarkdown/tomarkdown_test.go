package tomarkdown

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/dstotijn/go-notion"
	"github.com/stretchr/testify/assert"
)

//go:embed testdata
var testdatas embed.FS

func testTarget(t *testing.T, target string) {
	fs.WalkDir(testdatas, ".", func(path string, d fs.DirEntry, err error) error {
		if d.IsDir() {
			return nil
		}

		blockBytes, err := testdatas.ReadFile(path)
		assert.NoError(t, err)

		fmt.Printf("===== Testing %s =====\n", path)
		blocks := make([]notion.Block, 0)
		assert.NoError(t, json.Unmarshal(blockBytes, &blocks))
		tom := New()
		tom.ImgSavePath = "/tmp/"
		tom.EnableExtendedSyntax(target)
		assert.NoError(t, tom.GenerateTo(blocks, os.Stdout))
		return nil
	})
}

// TestBlockConversion tests the conversion of specific block types with golden files
func TestBlockConversion(t *testing.T) {
	// Test with default depth (0)
	testBlockConversionWithDepth(t, 0)
}

// TestBlockConversionWithDepth tests a specific block type at a given depth
func TestBlockConversionWithDepth(t *testing.T) {
	depths := []int{0, 1, 2} // Test multiple indentation levels
	for _, depth := range depths {
		t.Run(fmt.Sprintf("depth_%d", depth), func(t *testing.T) {
			testBlockConversionWithDepth(t, depth)
		})
	}
}

// testBlockConversionWithDepth is a helper function to test block conversion at a specific depth
func testBlockConversionWithDepth(t *testing.T, depth int) {
	// Walk through all JSON files in testdata directory
	err := fs.WalkDir(testdatas, ".", func(path string, d fs.DirEntry, err error) error {
		if d.IsDir() {
			return nil
		}

		// Only process JSON files
		if !strings.HasSuffix(path, ".json") {
			return nil
		}

		// Look for a corresponding .md file (the golden file)
		baseName := strings.TrimSuffix(path, ".json")
		goldenFileName := fmt.Sprintf("%s.md", baseName)

		// Check if golden file exists
		goldenFileData, err := testdatas.ReadFile(goldenFileName)
		if err != nil {
			// Skip test if no golden file exists
			t.Logf("Skipping %s: no golden file found", path)
			return nil
		}

		// If we're testing with depth > 0, look for a depth-specific golden file first
		if depth > 0 {
			depthGoldenFileName := fmt.Sprintf("%s_depth%d.md", baseName, depth)
			depthGoldenFileData, depthErr := testdatas.ReadFile(depthGoldenFileName)
			if depthErr == nil {
				// Use the depth-specific golden file if it exists
				goldenFileData = depthGoldenFileData
			}
		}

		expectedOutput := string(goldenFileData)

		// Read the JSON test file
		blockBytes, err := testdatas.ReadFile(path)
		if err != nil {
			t.Errorf("Failed to read test file %s: %v", path, err)
			return nil
		}

		// Unmarshal the JSON into Notion blocks
		blocks := make([]notion.Block, 0)
		if err := json.Unmarshal(blockBytes, &blocks); err != nil {
			t.Errorf("Failed to unmarshal test file %s: %v", path, err)
			return nil
		}

		// Create a function to directly render blocks with proper depth handling
		renderBlocksWithDepth := func(blocks []notion.Block, depth int) (string, error) {
			// Initialize the ToMarkdown converter with a fresh buffer
			tom := New()
			tom.ImgSavePath = "/tmp/"
			tom.ContentBuffer = new(bytes.Buffer) // Ensure we start with an empty buffer

			// For each block, create an MdBlock with the specified depth and render it directly
			for _, block := range blocks {
				mdBlock := MdBlock{
					Block: block,
					Depth: depth,
					Extra: make(map[string]interface{}),
				}

				// Render the block using the appropriate template
				if err := tom.GenBlock(block.Type, mdBlock); err != nil {
					return "", fmt.Errorf("failed to render block: %v", err)
				}
			}

			// Get the content as a string
			result := tom.ContentBuffer.String()

			// Remove the initial newline if present
			result = strings.TrimPrefix(result, "\n")

			return result, nil
		}

		// Generate the markdown
		actualOutput, err := renderBlocksWithDepth(blocks, depth)
		if err != nil {
			t.Errorf("Failed to render blocks for %s at depth %d: %v", path, depth, err)
			return nil
		}

		// Assert that the generated output matches the expected output
		if actualOutput != expectedOutput {
			t.Errorf("Output mismatch for %s at depth %d:\nExpected:\n%s\n\nActual:\n%s",
				path, depth, expectedOutput, actualOutput)

			if false { // this is only for debugging the tests
				// Add more detailed comparison for debugging
				t.Errorf("Detailed comparison:")
				t.Errorf("Expected length: %d, Actual length: %d", len(expectedOutput), len(actualOutput))

				// Print character codes for better visual inspection
				t.Errorf("Expected output character codes:")
				for i, c := range expectedOutput {
					t.Errorf("  Index %d: '%c' (Code: %d)", i, c, c)
				}

				t.Errorf("Actual output character codes:")
				for i, c := range actualOutput {
					t.Errorf("  Index %d: '%c' (Code: %d)", i, c, c)
				}

				// Find the first differing position
				minLen := len(expectedOutput)
				if len(actualOutput) < minLen {
					minLen = len(actualOutput)
				}

				for i := 0; i < minLen; i++ {
					if expectedOutput[i] != actualOutput[i] {
						t.Errorf("First difference at position %d: Expected '%c' (Code: %d), Actual '%c' (Code: %d)",
							i, expectedOutput[i], expectedOutput[i], actualOutput[i], actualOutput[i])
						break
					}
				}

				if len(expectedOutput) != len(actualOutput) && minLen == len(expectedOutput) {
					t.Errorf("Expected output is shorter. Actual has extra characters starting at position %d", minLen)
				} else if len(expectedOutput) != len(actualOutput) {
					t.Errorf("Expected output is longer. Expected has extra characters starting at position %d", minLen)
				}
			}
		} else {
			t.Logf("Successfully tested %s at depth %d", path, depth)
		}

		return nil
	})

	if err != nil {
		t.Fatalf("Failed to walk testdata directory: %v", err)
	}
}

func TestOne(t *testing.T) {
	testTarget(t, "vuepress")
}

func TestAllTarget(t *testing.T) {
	targets := []string{"hugo", "hexo", "vuepress"}
	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			testTarget(t, target)
		})
	}
}
