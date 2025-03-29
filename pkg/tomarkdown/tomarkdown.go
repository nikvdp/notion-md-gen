package tomarkdown

import (
	"bytes"
	"embed"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/Masterminds/sprig"
	"github.com/dstotijn/go-notion"
	"github.com/otiai10/opengraph"
	"gopkg.in/yaml.v3"
)

//go:embed templates
var mdTemplatesFS embed.FS

var (
	extendedSyntaxBlocks = []notion.BlockType{
		notion.BlockTypeBookmark,
		notion.BlockTypeCallout,
	}
	blockTypeInExtendedSyntaxBlocks = func(bType notion.BlockType) bool {
		for _, blockType := range extendedSyntaxBlocks {
			if blockType == bType {
				return true
			}
		}
		return false
	}
)

type MdBlock struct {
	notion.Block
	Depth int
	Extra map[string]interface{}
}

type ToMarkdown struct {
	FrontMatter     map[string]interface{}
	ContentBuffer   *bytes.Buffer
	ImgSavePath     string
	ImgVisitPath    string
	ContentTemplate string

	extra map[string]interface{}
}

func New() *ToMarkdown {
	return &ToMarkdown{
		FrontMatter:   make(map[string]interface{}),
		ContentBuffer: new(bytes.Buffer),
		extra:         make(map[string]interface{}),
	}
}

// WithFrontMatter loads data from the Notion page (like cover or custom properties)
// into the front matter map.
func (tm *ToMarkdown) WithFrontMatter(page notion.Page) {
	tm.injectFrontMatterCover(page.Cover)
	pageProps := page.Properties.(notion.DatabasePageProperties)
	for fmKey, property := range pageProps {
		tm.injectFrontMatter(fmKey, property)
	}
}

// EnableExtendedSyntax instructs the renderer to handle blocks (like Bookmark, Callout)
// with custom shortcodes for Hugo/Hexo/Vuepress.
func (tm *ToMarkdown) EnableExtendedSyntax(target string) {
	tm.extra["ExtendedSyntaxEnabled"] = true
	tm.extra["ExtendedSyntaxTarget"] = target
}

// ExtendedSyntaxEnabled checks if extended syntax is enabled
func (tm *ToMarkdown) ExtendedSyntaxEnabled() bool {
	if v, ok := tm.extra["ExtendedSyntaxEnabled"].(bool); ok {
		return v
	}
	return false
}

// shouldSkipRender returns true if the given block type should be ignored
// unless we've explicitly enabled extended syntax
func (tm *ToMarkdown) shouldSkipRender(bType notion.BlockType) bool {
	return !tm.ExtendedSyntaxEnabled() && blockTypeInExtendedSyntaxBlocks(bType)
}

// GenerateTo renders the blocks into Markdown, writing front matter first (if any),
// then the block content into the provided writer.
func (tm *ToMarkdown) GenerateTo(blocks []notion.Block, writer io.Writer) error {
	// front matter
	if err := tm.GenFrontMatter(writer); err != nil {
		return err
	}

	// block content
	if err := tm.GenContentBlocks(blocks, 0); err != nil {
		return err
	}

	// If a custom ContentTemplate is provided, run the final content through that template
	if tm.ContentTemplate != "" {
		t, err := template.ParseFiles(tm.ContentTemplate)
		if err != nil {
			return err
		}
		return t.Execute(writer, tm)
	}

	// Otherwise, just copy from the buffer
	_, err := io.Copy(writer, tm.ContentBuffer)
	return err
}

// GenFrontMatter marshals any front matter set in tm.FrontMatter to YAML and
// writes it at the top of the file under triple-dashed lines.
func (tm *ToMarkdown) GenFrontMatter(writer io.Writer) error {
	if len(tm.FrontMatter) == 0 {
		return nil
	}
	nfm := make(map[string]interface{})
	for key, value := range tm.FrontMatter {
		nfm[strings.ToLower(key)] = value
	}

	frontMatters, err := yaml.Marshal(nfm)
	if err != nil {
		return nil
	}

	buffer := new(bytes.Buffer)
	buffer.WriteString("---\n")
	buffer.Write(frontMatters)
	buffer.WriteString("---\n\n")
	_, err = io.Copy(writer, buffer)
	return err
}

// GenContentBlocks iterates over each block and calls GenBlock. The 'depth' indicates
// nesting level for indentation, etc. If a block has children, we recursively
// render them at depth+1 afterwards.
func (tm *ToMarkdown) GenContentBlocks(blocks []notion.Block, depth int) error {
	var sameBlockIdx int
	var lastBlockType notion.BlockType

	for _, block := range blocks {
		if tm.shouldSkipRender(block.Type) {
			continue
		}
		sameBlockIdx++
		if block.Type != lastBlockType {
			sameBlockIdx = 0
		}

		mdb := MdBlock{
			Block: block,
			Depth: depth,
			Extra: tm.extra,
		}
		mdb.Extra["SameBlockIdx"] = sameBlockIdx

		// Some pre-processing, e.g. for images or bookmarks
		switch block.Type {
		case notion.BlockTypeImage:
			if err := tm.downloadImage(block.Image); err != nil {
				return err
			}
		case notion.BlockTypeBookmark:
			if err := tm.injectBookmarkInfo(block.Bookmark, &mdb.Extra); err != nil {
				return err
			}
		}

		// Render the block
		if err := tm.GenBlock(block.Type, mdb); err != nil {
			return err
		}

		lastBlockType = block.Type
	}
	return nil
}

// GenBlock executes the relevant template for the block type, appending
// the output to tm.ContentBuffer. If block.HasChildren, we recursively process
// its child blocks, at (depth+1).
func (tm *ToMarkdown) GenBlock(bType notion.BlockType, block MdBlock) error {
	funcs := sprig.TxtFuncMap()
	funcs["deref"] = func(i *bool) bool { return *i }
	funcs["rich2md"] = ConvertRichText
	funcs["indentCode"] = func(richText []notion.RichText, depth int) string {
		// Get the content without any manipulation
		content := ConvertRichText(richText)
		
		// If depth is 0, no indentation needed
		if depth == 0 {
			return content
		}
		
		// Apply indentation based on depth
		indent := strings.Repeat("  ", depth)
		
		// Split into lines for processing
		lines := strings.Split(content, "\n")
		
		// Apply the correct indentation to each line
		for i := 0; i < len(lines); i++ {
			lines[i] = indent + lines[i]
		}
		
		// Join lines back together
		return strings.Join(lines, "\n")
	}

	tplName := fmt.Sprintf("%s.gohtml", bType)
	t := template.New(tplName).Funcs(funcs)

	tpl, err := t.ParseFS(mdTemplatesFS, "templates/"+tplName)
	if err != nil {
		// If no template for that block type, skip gracefully
		return nil
	}

	if err := tpl.Execute(tm.ContentBuffer, block); err != nil {
		return err
	}

	// If the block has child blocks, render them now at depth+1
	if block.HasChildren {
		if err := tm.GenContentBlocks(getChildrenBlocks(block), block.Depth+1); err != nil {
			return err
		}
	}
	return nil
}

// downloadImage fetches the external image or file-based image, saves it locally, and updates its URL
func (tm *ToMarkdown) downloadImage(image *notion.FileBlock) error {
	download := func(imgURL string) (string, error) {
		resp, err := http.Get(imgURL)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		return tm.saveTo(resp.Body, imgURL, tm.ImgSavePath)
	}

	var err error
	if image.Type == notion.FileTypeExternal {
		var newURL string
		newURL, err = download(image.External.URL)
		if err != nil {
			return err
		}
		image.External.URL = newURL
	}
	if image.Type == notion.FileTypeFile {
		var newURL string
		newURL, err = download(image.File.URL)
		if err != nil {
			return err
		}
		image.File.URL = newURL
	}
	return err
}

// saveTo saves the content of reader into distDir, generating a filename from
// rawURL. Returns the final new path for the local or site image usage.
func (tm *ToMarkdown) saveTo(reader io.Reader, rawURL, distDir string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("malformed url: %s", err)
	}

	splitPaths := strings.Split(u.Path, "/")
	imageFilename := splitPaths[len(splitPaths)-1]
	if strings.HasPrefix(imageFilename, "Untitled.") {
		imageFilename = splitPaths[len(splitPaths)-2] + filepath.Ext(u.Path)
	}
	if err := os.MkdirAll(distDir, 0755); err != nil {
		return "", fmt.Errorf("%s: %s", distDir, err)
	}

	// Create a unique filename using the full URL path to avoid collisions
	urlPath := strings.Join(splitPaths, "_")
	filename := fmt.Sprintf("%s_%s_%s", u.Hostname(), urlPath, imageFilename)
	out, err := os.Create(filepath.Join(distDir, filename))
	if err != nil {
		return "", fmt.Errorf("couldn't create image file: %s", err)
	}
	defer out.Close()

	_, err = io.Copy(out, reader)
	if err != nil {
		return "", err
	}
	return filepath.Join(tm.ImgVisitPath, filename), nil
}

// injectBookmarkInfo sets image, title, and description from opengraph into the block's Extra map
func (tm *ToMarkdown) injectBookmarkInfo(bookmark *notion.Bookmark, extra *map[string]interface{}) error {
	og, err := opengraph.Fetch(bookmark.URL)
	if err != nil {
		return err
	}
	og.ToAbsURL()
	for _, img := range og.Image {
		if img != nil && img.URL != "" {
			(*extra)["Image"] = img.URL
			break
		}
	}
	(*extra)["Title"] = og.Title
	(*extra)["Description"] = og.Description
	return nil
}

// injectFrontMatter converts a Notion property into front matter data
func (tm *ToMarkdown) injectFrontMatter(key string, property notion.DatabasePageProperty) {
	var fmv interface{}
	switch prop := property.Value().(type) {
	case *notion.SelectOptions:
		if prop != nil {
			fmv = prop.Name
		}
	case []notion.SelectOptions:
		opts := make([]string, 0, len(prop))
		for _, options := range prop {
			opts = append(opts, options.Name)
		}
		fmv = opts
	case []notion.RichText:
		fmv = ConvertRichText(prop)
	case *time.Time:
		if prop != nil {
			fmv = prop.Format("2006-01-02T15:04:05+07:00")
		}
	case *notion.Date:
		if prop != nil {
			if !prop.Start.IsZero() {
				fmv = prop.Start.Format("2006-01-02T15:04:05+07:00")
			} else if !prop.End.IsZero() {
				fmv = prop.End.Format("2006-01-02T15:04:05+07:00")
			}
		}
	case *notion.User:
		if prop != nil {
			fmv = prop.Name
		}
	case *string:
		if prop != nil {
			fmv = *prop
		}
	case *float64:
		if prop != nil {
			fmv = *prop
		}
	default:
		// ignoring unsupported prop type
	}
	if fmv != nil {
		tm.FrontMatter[key] = fmv
	}
}

// injectFrontMatterCover downloads the page cover image and sets the front matter "cover" field
func (tm *ToMarkdown) injectFrontMatterCover(cover *notion.Cover) {
	if cover == nil {
		return
	}
	image := &notion.FileBlock{
		Type:     cover.Type,
		File:     cover.File,
		External: cover.External,
	}
	if err := tm.downloadImage(image); err != nil {
		return
	}
	if image.Type == notion.FileTypeExternal {
		tm.FrontMatter["cover"] = image.External.URL
	} else if image.Type == notion.FileTypeFile {
		tm.FrontMatter["cover"] = image.File.URL
	}
}

// ConvertRichText joins multiple RichText objects into a single string
func ConvertRichText(t []notion.RichText) string {
	var buf bytes.Buffer
	for _, word := range t {
		content := ConvertRich(word)
		buf.WriteString(content)
	}
	return buf.String()
}

// ConvertRich returns a single RichText as Markdown
func ConvertRich(t notion.RichText) string {
	switch t.Type {
	case notion.RichTextTypeText:
		if t.Text.Link != nil {
			return fmt.Sprintf(emphFormat(t.Annotations),
				fmt.Sprintf("[%s](%s)", t.Text.Content, t.Text.Link.URL))
		}
		return fmt.Sprintf(emphFormat(t.Annotations), t.Text.Content)
	case notion.RichTextTypeEquation:
		// Not currently handled, skip or add your own format
	case notion.RichTextTypeMention:
		// Possibly format mention
	}
	return ""
}

// emphFormat generates markdown emphasis from annotations
func emphFormat(a *notion.Annotations) string {
	s := "%s"
	if a == nil {
		return s
	}
	if a.Code {
		return "`%s`"
	}
	switch {
	case a.Bold && a.Italic:
		s = "***%s***"
	case a.Bold:
		s = "**%s**"
	case a.Italic:
		s = "*%s*"
	}
	if a.Underline {
		s = "__" + s + "__"
	} else if a.Strikethrough {
		s = "~~" + s + "~~"
	}
	// color is ignored in basic Markdown
	return s
}

// getChildrenBlocks extracts the child blocks from a given block
func getChildrenBlocks(block MdBlock) []notion.Block {
	switch block.Type {
	case notion.BlockTypeQuote:
		return block.Quote.Children
	case notion.BlockTypeToggle:
		return block.Toggle.Children
	case notion.BlockTypeParagraph:
		return block.Paragraph.Children
	case notion.BlockTypeCallout:
		return block.Callout.Children
	case notion.BlockTypeBulletedListItem:
		return block.BulletedListItem.Children
	case notion.BlockTypeNumberedListItem:
		return block.NumberedListItem.Children
	case notion.BlockTypeToDo:
		return block.ToDo.Children
	case notion.BlockTypeCode:
		return block.Code.Children
	case notion.BlockTypeColumn:
		return block.Column.Children
	case notion.BlockTypeColumnList:
		return block.ColumnList.Children
	case notion.BlockTypeTable:
		return block.Table.Children
	case notion.BlockTypeSyncedBlock:
		return block.SyncedBlock.Children
	case notion.BlockTypeTemplate:
		return block.Template.Children
	default:
		return nil
	}
}
