package generator

import (
	"fmt"
	"io/fs"
	"io/ioutil"

	"gopkg.in/yaml.v3"
)

type Notion struct {
	DatabaseID     string   `yaml:"databaseId"`
	FilterProp     string   `yaml:"filterProp"`
	FilterValue    []string `yaml:"filterValue"`
	PublishedValue string   `yaml:"publishedValue"`
}

type Markdown struct {
	ShortcodeSyntax string `yaml:"shortcodeSyntax"` // hugo,hexo,vuepress
	PageNamePrefix  string `yaml:"pageNamePrefix"`
	PostSavePath    string `yaml:"postSavePath"`
	ImageSavePath   string `yaml:"imageSavePath"`
	ImagePublicLink string `yaml:"imagePublicLink"`

	// Optional:
	GroupByMonth bool   `yaml:"groupByMonth,omitempty"`
	Template     string `yaml:"template,omitempty"`
}

type Config struct {
	Notion   `yaml:"notion"`
	Markdown `yaml:"markdown"`
	// enable parallel fetching of block trees
	Parallelize bool `yaml:"parallelize"`
	// number of concurrent block tree fetches
	Parallelism int `yaml:"parallelism"`
}

func DefaultConfigInit() error {
	defaultCfg := &Config{
		Notion: Notion{
			DatabaseID:     "YOUR-NOTION-DATABASE-ID",
			FilterProp:     "Status",
			FilterValue:    []string{"Finished", "Published"},
			PublishedValue: "Published",
		},
		Markdown: Markdown{
			ShortcodeSyntax: "vuepress",
			PostSavePath:    "posts/notion",
			ImageSavePath:   "static/images/notion",
			ImagePublicLink: "/images/notion",
		},
		// enable parallelization by default
		Parallelize: true,
		// default to 4 concurrent fetches
		Parallelism: 4,
	}
	out, err := yaml.Marshal(defaultCfg)
	if err != nil {
		return err
	}

	defer func() {
		_ = ioutil.WriteFile(".env", []byte("NOTION_SECRET=xxxx"), 0644)
		fmt.Println("Config file notion-md-gen.yaml and .env created, please edit them for yourself.")
	}()

	return ioutil.WriteFile("notion-md-gen.yaml", out, fs.FileMode(0644))
}
