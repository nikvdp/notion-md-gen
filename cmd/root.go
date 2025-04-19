package cmd

import (
	"fmt"
	"log"
	"os"

	"github.com/bonaysoft/notion-md-gen/generator"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var cfgFile string

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "notion-md-gen",
	Short: "A markdown generator for notion",
	// Uncomment the following line if your bare application
	// has an action associated with it:
	Run: func(cmd *cobra.Command, args []string) {
		var config generator.Config
		if err := viper.Unmarshal(&config); err != nil {
			log.Fatal(err)
		}

		// set parallelization options from viper flags
		config.Parallelism = viper.GetInt("parallelism")
		// if parallelism is 0, force serial mode
		if config.Parallelism == 0 {
			config.Parallelize = false
			config.Parallelism = 1 // not used, but keep safe
		} else {
			config.Parallelize = viper.GetBool("parallelize")
		}

		if err := generator.Run(config); err != nil {
			log.Println(err)
		}
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is notion-md-gen.yaml)")
	// add flag to enable/disable parallelization
	rootCmd.PersistentFlags().Bool("parallelize", true, "enable parallel fetching of block trees")
	// add flag to set parallelism level, with short version -j
	rootCmd.PersistentFlags().IntP("parallelism", "j", 5, "number of concurrent block tree fetches (use 0 for serial mode)")
	// bind flags to viper
	_ = viper.BindPFlag("parallelize", rootCmd.PersistentFlags().Lookup("parallelize"))
	_ = viper.BindPFlag("parallelism", rootCmd.PersistentFlags().Lookup("parallelism"))
}

// initConfig reads in config file and ENV variables if set.
func initConfig() {
	if cfgFile != "" {
		// Use config file from the flag.
		viper.SetConfigFile(cfgFile)
	} else {
		viper.AddConfigPath(".")
		viper.SetConfigName("notion-md-gen")
	}

	if err := godotenv.Load(); err == nil {
		fmt.Println("Load .env file")
	}

	viper.AutomaticEnv() // read in environment variables that match

	// If a config file is found, read it in.
	if err := viper.ReadInConfig(); err == nil {
		fmt.Println("Using config file:", viper.ConfigFileUsed())
	}
}
