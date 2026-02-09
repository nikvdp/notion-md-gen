package generator

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bonaysoft/notion-md-gen/pkg/tomarkdown"
	"github.com/hashicorp/go-retryablehttp"

	"github.com/dstotijn/go-notion"
)

// getpagetitle extracts the plain text title from page properties.
func getPageTitle(page notion.Page) string {
	props, ok := page.Properties.(notion.DatabasePageProperties)
	if !ok {
		return "" // or page.id if preferred as fallback
	}
	// look for a property named "title" or "name" case-insensitively
	for key, prop := range props {
		if prop.Type == notion.DBPropTypeTitle && (strings.EqualFold(key, "title") || strings.EqualFold(key, "name")) {
			return tomarkdown.ConvertRichText(prop.Title)
		}
	}
	// fallback: check all title properties just in case
	for _, prop := range props {
		if prop.Type == notion.DBPropTypeTitle && len(prop.Title) > 0 {
			title := tomarkdown.ConvertRichText(prop.Title)
			if title != "" {
				return title
			}
		}
	}
	return "" // no title found
}

func Run(config Config, filterArgs []string, since *time.Time, dryRun bool) error {
	if config.CacheFile == "" {
		config.CacheFile = ".notion-md-gen-cache.json"
	}

	if err := os.MkdirAll(config.Markdown.PostSavePath, 0755); err != nil {
		// even in dry run, we might need the path conceptually, but check if it exists
		// let's not create it if dry-run, maybe check later if needed?
		// for now, let's skip creating the dir in dry-run mode.
		if !dryRun {
			return fmt.Errorf("couldn't create content folder: %s", err)
		}
	}

	// find database page
	client := notion.NewClient(os.Getenv("NOTION_SECRET"), notion.WithHTTPClient(retryablehttp.NewClient().StandardClient()))
	q, err := queryDatabase(client, config.Notion)
	if err != nil {
		return fmt.Errorf("❌ Querying Notion database: %s", err)
	}
	fmt.Println("✔ Querying Notion database: Completed")

	// filter pages based on args and --since flag
	pagesToProcess := []notion.Page{}
	filterActive := len(filterArgs) > 0 || since != nil
	if filterActive {
		if len(filterArgs) > 0 {
			fmt.Printf("Filtering pages by keywords: %v\n", filterArgs)
		}
		if since != nil {
			// fmt.Printf("Filtering pages modified since: %s\n", since.Format(time.RFC3339)) // already printed in root.go
		}
		for _, page := range q.Results {
			// --since filter (last edited time)
			if since != nil && !page.LastEditedTime.After(*since) {
				continue
			}

			// title keyword filter
			if len(filterArgs) > 0 {
				pageTitle := getPageTitle(page)
				if pageTitle == "" {
					continue // skip pages without a title for keyword filtering
				}
				lowerTitle := strings.ToLower(pageTitle)
				matchAllKeywords := true
				for _, arg := range filterArgs {
					if !strings.Contains(lowerTitle, strings.ToLower(arg)) {
						matchAllKeywords = false
						break
					}
				}
				if !matchAllKeywords {
					continue // skip if title doesn't match all keywords
				}
			}

			// if we got here, the page passed all active filters
			pagesToProcess = append(pagesToProcess, page)
		}
		fmt.Printf("✔ Filtering completed: %d pages matched\n", len(pagesToProcess))
	} else {
		pagesToProcess = q.Results // no filters, process all pages
	}

	if len(pagesToProcess) == 0 {
		fmt.Println("No pages found matching the criteria.")
		return nil // exit gracefully if no pages match
	}

	cache := defaultCache()
	if config.Incremental {
		loadedCache, err := loadCache(config.CacheFile)
		if err != nil {
			return fmt.Errorf("failed loading cache file %q: %w", config.CacheFile, err)
		}
		cache = loadedCache
	}

	unchangedSkipped := 0
	filteredPages := make([]notion.Page, 0, len(pagesToProcess))
	for _, page := range pagesToProcess {
		title := getPageTitle(page)
		if title == "" {
			title = page.ID
		}
		outputRelPath := generateArticleFilename(title, page.CreatedTime, config.Markdown)
		outputAbsPath := filepath.Join(config.Markdown.PostSavePath, outputRelPath)
		pageEditedAt := cacheTimestamp(page.LastEditedTime)

		entry, found := cache.Pages[page.ID]
		skipAsUnchanged := config.Incremental && found && entry.LastEdited == pageEditedAt
		if skipAsUnchanged {
			if _, err := os.Stat(outputAbsPath); err == nil {
				unchangedSkipped++
				continue
			}
		}
		filteredPages = append(filteredPages, page)
	}
	pagesToProcess = filteredPages

	// handle dry run: print titles and exit
	if dryRun {
		fmt.Println("\n-- Dry Run Active --")
		fmt.Println("Articles that would be processed:")
		for i, page := range pagesToProcess {
			title := getPageTitle(page)
			if title == "" {
				title = "[Untitled Page: " + page.ID + "]"
			}
			fmt.Printf("  %d: %s (ID: %s, LastEdited: %s)\n", i+1, title, page.ID, page.LastEditedTime.Local().Format(time.RFC822))
		}
		return nil
	}

	if config.Incremental && unchangedSkipped > 0 {
		fmt.Printf("✔ Incremental sync: skipped %d unchanged pages\n", unchangedSkipped)
	}

	if len(pagesToProcess) == 0 {
		fmt.Println("No changed pages to process.")
		return nil
	}

	// helper to fetch, generate, and update status for a page (only runs if not dryRun)
	handlePage := func(page notion.Page, blocks []notion.Block, displayName string, previousOutputRelPath string) (string, error) {
		fmt.Printf("[%-30s] ✔ getting blocks tree: completed\n", displayName)
		title := getPageTitle(page)
		if title == "" {
			title = page.ID
		}
		outputRelPath := generateArticleFilename(title, page.CreatedTime, config.Markdown)
		outputAbsPath := filepath.Join(config.Markdown.PostSavePath, outputRelPath)

		if config.Incremental && previousOutputRelPath != "" && previousOutputRelPath != outputRelPath {
			previousFile := filepath.Join(config.Markdown.PostSavePath, previousOutputRelPath)
			if _, err := os.Stat(previousFile); err == nil {
				_ = os.Remove(previousFile)
			}
		}

		if err := generate(page, blocks, config.Markdown, outputAbsPath, title); err != nil {
			return "", fmt.Errorf("[%-30s] error generating blog post: %v", displayName, err)
		}
		fmt.Printf("[%-30s] ✔ generating blog post: completed\n", displayName)
		return outputRelPath, nil
	}

	changed := 0 // number of article status changed

	if config.Parallelize {
		// fetch and render pages in parallel using a bounded semaphore
		sem := make(chan struct{}, config.Parallelism)
		errCh := make(chan error, len(pagesToProcess))
		var wg sync.WaitGroup
		var mu sync.Mutex

		for i, page := range pagesToProcess {
			displayName := getPageDisplayName(i, page)
			sem <- struct{}{}
			wg.Add(1)
			go func(i int, page notion.Page, displayName string) {
				defer wg.Done()
				defer func() { <-sem }()
				fmt.Printf("[%-30s] -- article [%d/%d] --\n", displayName, i+1, len(pagesToProcess))
				blocks, err := queryBlockChildren(client, page.ID)
				if err != nil {
					errCh <- fmt.Errorf("[%-30s] error getting blocks: %v", displayName, err)
					return
				}
				var previousOutputRelPath string
				if config.Incremental {
					if prev, ok := cache.Pages[page.ID]; ok {
						previousOutputRelPath = prev.OutputPath
					}
				}
				outputRelPath, err := handlePage(page, blocks, displayName, previousOutputRelPath)
				if err != nil {
					errCh <- err
					return
				}
				statusChanged := changeStatus(client, page, config.Notion)
				mu.Lock()
				cache.Pages[page.ID] = cacheEntry{
					LastEdited: cacheTimestamp(page.LastEditedTime),
					OutputPath: outputRelPath,
				}
				if statusChanged {
					changed++
				}
				mu.Unlock()
			}(i, page, displayName)
		}
		wg.Wait()
		close(errCh)
		for err := range errCh {
			if err != nil {
				return err
			}
		}
	} else {
		// sequential fallback
		for i, page := range pagesToProcess {
			displayName := getPageDisplayName(i, page)
			fmt.Printf("[%-30s] -- article [%d/%d] --\n", displayName, i+1, len(pagesToProcess))
			blocks, err := queryBlockChildren(client, page.ID)
			if err != nil {
				return fmt.Errorf("[%-30s] error getting blocks: %v", displayName, err)
			}
			var previousOutputRelPath string
			if config.Incremental {
				if prev, ok := cache.Pages[page.ID]; ok {
					previousOutputRelPath = prev.OutputPath
				}
			}
			outputRelPath, err := handlePage(page, blocks, displayName, previousOutputRelPath)
			if err != nil {
				return err
			}
			cache.Pages[page.ID] = cacheEntry{
				LastEdited: cacheTimestamp(page.LastEditedTime),
				OutputPath: outputRelPath,
			}
			if changeStatus(client, page, config.Notion) {
				changed++
			}
		}
	}

	if config.Incremental {
		if err := saveCache(config.CacheFile, cache); err != nil {
			return fmt.Errorf("failed writing cache file %q: %w", config.CacheFile, err)
		}
		fmt.Printf("✔ Cache updated: %s\n", config.CacheFile)
	}

	fmt.Printf("✔ Sync complete: processed=%d, skipped=%d, status-updated=%d\n", len(pagesToProcess), unchangedSkipped, changed)

	return nil
}

func generate(page notion.Page, blocks []notion.Block, config Markdown, outputAbsPath string, pageName string) error {
	// Create file

	// fmt.Println("Page: ", page.Properties.(notion.DatabasePageProperties)["title"].Title)
	// fmt.Println("Title: ", page.Properties.(notion.DatabasePageProperties)["title"].Title[0].Text.Content)
	// pageName := config.PageNamePrefix + tomarkdown.ConvertRichText(page.Properties.(notion.DatabasePageProperties)["Name"].Title)
	f, err := os.Create(outputAbsPath)
	if err != nil {
		return fmt.Errorf("error create file: %s", err)
	}
	defer f.Close()

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
	// use the new helper function to get the title
	title := getPageTitle(page)
	if title != "" {
		return fmt.Sprintf("%d:%s", i+1, title)
	}

	// fallback to id if title extraction failed
	return fmt.Sprintf("%d:%s", i+1, page.ID)
}
