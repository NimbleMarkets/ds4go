package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/NimbleMarkets/ds4go/webtool"
	"github.com/spf13/cobra"
)

func newWebCommand() *cobra.Command {
	var port int
	var chromePath string
	var provider string

	cmd := &cobra.Command{
		Use:   "web",
		Short: "Test browser-backed web tools",
		Long:  "Commands to test the search and page extraction web helpers directly.",
	}

	cmd.PersistentFlags().IntVar(&port, "port", 9333, "debugging port for Chrome")
	cmd.PersistentFlags().StringVar(&chromePath, "chrome-path", "", "explicit path to Chrome/Chromium executable")

	searchCmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Execute web search and print Markdown links",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := strings.Join(args, " ")
			helper := webtool.NewWebHelper(webtool.Config{
				Port:       port,
				ChromePath: chromePath,
				ConfirmApproval: func(message string) (bool, error) {
					fmt.Print(message)
					var input string
					_, err := fmt.Scanln(&input)
					if err != nil {
						return false, err
					}
					input = strings.ToLower(strings.TrimSpace(input))
					return input == "y" || input == "yes", nil
				},
				Log: func(msg string) {
					fmt.Printf("[Status] %s\n", msg)
				},
			})
			defer helper.Close()

			fmt.Printf("Searching for %q using %s...\n", query, provider)
			res, err := helper.Search(context.Background(), query, provider)
			if err != nil {
				return err
			}
			fmt.Println("\n--- Search Results ---")
			fmt.Println(res)
			return nil
		},
	}

	searchCmd.Flags().StringVar(&provider, "provider", "google", "search engine to use: google, duckduckgo, bing, yahoo, yandex, baidu, brave")

	visitCmd := &cobra.Command{
		Use:   "visit <url>",
		Short: "Visit a web page and print extracted Markdown",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			targetURL := args[0]
			helper := webtool.NewWebHelper(webtool.Config{
				Port:       port,
				ChromePath: chromePath,
				ConfirmApproval: func(message string) (bool, error) {
					fmt.Print(message)
					var input string
					_, err := fmt.Scanln(&input)
					if err != nil {
						return false, err
					}
					input = strings.ToLower(strings.TrimSpace(input))
					return input == "y" || input == "yes", nil
				},
				Log: func(msg string) {
					fmt.Printf("[Status] %s\n", msg)
				},
			})
			defer helper.Close()

			fmt.Printf("Visiting %s...\n", targetURL)
			res, err := helper.VisitPage(context.Background(), targetURL)
			if err != nil {
				return err
			}
			fmt.Println("\n--- Page Content ---")
			fmt.Println(res)
			return nil
		},
	}

	cmd.AddCommand(searchCmd, visitCmd)
	return cmd
}
