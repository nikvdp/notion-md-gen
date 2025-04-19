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
	handlePage := func(i int, page notion.Page, blocks []notion.Block) error {
		fmt.Println("✔ Getting blocks tree: Completed")
		if err := generate(page, blocks, config.Markdown); err != nil {
			return fmt.Errorf("error generating blog post: %v", err)
		}
		fmt.Println("✔ Generating blog post: Completed")
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
			i      int
			page   notion.Page
			blocks []notion.Block
			err    error
		}
		results := make(chan result, len(q.Results))
		for i, page := range q.Results {
			sem <- struct{}{}
			go func(i int, page notion.Page) {
				defer func() { <-sem }()
				pageName := tomarkdown.ConvertRichText(page.Properties.(notion.DatabasePageProperties)["Name"].Title)
				fmt.Printf("-- Article [%d/%d] %s --\n", i+1, len(q.Results), pageName)
				blocks, err := queryBlockChildren(client, page.ID)
				results <- result{i, page, blocks, err}
			}(i, page)
		}
		// wait for all
		for i := 0; i < len(q.Results); i++ {
			res := <-results
			if res.err != nil {
				return fmt.Errorf("error getting blocks: %v", res.err)
			}
			if err := handlePage(res.i, res.page, res.blocks); err != nil {
				return err
			}
			if changeStatus(client, res.page, config.Notion) {
				changed++
			}
		}
	} else {
		// sequential fallback
		for i, page := range q.Results {
			pageName := tomarkdown.ConvertRichText(page.Properties.(notion.DatabasePageProperties)["Name"].Title)
			fmt.Printf("-- Article [%d/%d] %s --\n", i+1, len(q.Results), pageName)
			blocks, err := queryBlockChildren(client, page.ID)
			if err != nil {
				return fmt.Errorf("error getting blocks: %v", err)
			}
			if err := handlePage(i, page, blocks); err != nil {
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
