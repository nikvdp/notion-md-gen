package generator

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bonaysoft/notion-md-gen/pkg/tomarkdown"
	"github.com/hashicorp/go-retryablehttp"

	"github.com/dstotijn/go-notion"
)

func Run(config Config) error {
	if err := os.MkdirAll(config.Markdown.PostSavePath, 0755); err != nil {
		return fmt.Errorf("couldn't create content folder: %s", err)
	}

	// find database page
	client := notion.NewClient(os.Getenv("NOTION_SECRET"), notion.WithHTTPClient(retryablehttp.NewClient().StandardClient()))
	q, err := queryDatabase(client, config.Notion)
	if err != nil {
		return fmt.Errorf("❌ Querying Notion database: %s", err)
	}
	fmt.Println("✔ Querying Notion database: Completed")

	// helper to fetch, generate, and update status for a page
	handlePage := func(i int, page notion.Page, blocks []notion.Block, displayName string) error {
		fmt.Printf("[%-30s] ✔ getting blocks tree: completed\n", displayName)
		if err := generate(page, blocks, config.Markdown); err != nil {
			return fmt.Errorf("[%-30s] error generating blog post: %v", displayName, err)
		}
		fmt.Printf("[%-30s] ✔ generating blog post: completed\n", displayName)
		if changeStatus(client, page, config.Notion) {
			// changed++ // not needed outside
		}
		return nil
	}

	changed := 0 // number of article status changed

	if config.Parallelize {
		// fetch block trees in parallel using a semaphore
		sem := make(chan struct{}, config.Parallelism)
		type result struct {
			i           int
			page        notion.Page
			blocks      []notion.Block
			err         error
			displayName string
		}
		results := make(chan result, len(q.Results))
		for i, page := range q.Results {
			displayName := getPageDisplayName(i, page)
			sem <- struct{}{}
			go func(i int, page notion.Page, displayName string) {
				defer func() { <-sem }()
				fmt.Printf("[%-30s] -- article [%d/%d] --\n", displayName, i+1, len(q.Results))
				blocks, err := queryBlockChildren(client, page.ID)
				results <- result{i, page, blocks, err, displayName}
			}(i, page, displayName)
		}
		// wait for all
		for i := 0; i < len(q.Results); i++ {
			res := <-results
			if res.err != nil {
				return fmt.Errorf("[%-30s] error getting blocks: %v", res.displayName, res.err)
			}
			if err := handlePage(res.i, res.page, res.blocks, res.displayName); err != nil {
				return err
			}
			if changeStatus(client, res.page, config.Notion) {
				changed++
			}
		}
	} else {
		// sequential fallback
		for i, page := range q.Results {
			displayName := getPageDisplayName(i, page)
			fmt.Printf("[%-30s] -- article [%d/%d] --\n", displayName, i+1, len(q.Results))
			blocks, err := queryBlockChildren(client, page.ID)
			if err != nil {
				return fmt.Errorf("[%-30s] error getting blocks: %v", displayName, err)
			}
			if err := handlePage(i, page, blocks, displayName); err != nil {
				return err
			}
			if changeStatus(client, page, config.Notion) {
				changed++
			}
		}
	}

	return nil
}

func generate(page notion.Page, blocks []notion.Block, config Markdown) error {
	// Create file

	// fmt.Println("Page: ", page.Properties.(notion.DatabasePageProperties)["title"].Title)
	// fmt.Println("Title: ", page.Properties.(notion.DatabasePageProperties)["title"].Title[0].Text.Content)
	// pageName := config.PageNamePrefix + tomarkdown.ConvertRichText(page.Properties.(notion.DatabasePageProperties)["Name"].Title)
	pageName := tomarkdown.ConvertRichText(page.Properties.(notion.DatabasePageProperties)["Title"].Title)
	f, err := os.Create(filepath.Join(config.PostSavePath, generateArticleFilename(pageName, page.CreatedTime, config)))
	if err != nil {
		return fmt.Errorf("error create file: %s", err)
	}

	// Generate markdown content to the file
	tm := tomarkdown.New()
	tm.ImgSavePath = filepath.Join(config.ImageSavePath, pageName)
	tm.ImgVisitPath = filepath.Join(config.ImagePublicLink, url.PathEscape(pageName))
	tm.ContentTemplate = config.Template
	tm.WithFrontMatter(page)
	if config.ShortcodeSyntax != "" {
		tm.EnableExtendedSyntax(config.ShortcodeSyntax)
	}

	return tm.GenerateTo(blocks, f)
}

func generateArticleFilename(title string, date time.Time, config Markdown) string {
	escapedTitle := strings.ReplaceAll(
		strings.ToValidUTF8(
			strings.ToLower(title),
			"",
		),
		" ", "-",
	)
	escapedFilename := escapedTitle + ".md"

	if config.GroupByMonth {
		return filepath.Join(date.Format("2006-01-02"), escapedFilename)
	}

	return escapedFilename
}

// getPageDisplayName returns a display name for a page: [index:PageName] or [index:PageID] if no name
func getPageDisplayName(i int, page notion.Page) string {
	props, ok := page.Properties.(notion.DatabasePageProperties)
	if ok {
		for _, prop := range props {
			if prop.Type == notion.DBPropTypeTitle && len(prop.Title) > 0 {
				title := tomarkdown.ConvertRichText(prop.Title)
				if title != "" {
					return fmt.Sprintf("%d:%s", i+1, title)
				}
			}
		}
	}
	return fmt.Sprintf("%d:%s", i+1, page.ID)
}
