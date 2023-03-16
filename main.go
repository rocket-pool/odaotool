package main

import (
	"fmt"
	"net/http"
	"os"

	"github.com/fatih/color"
	"github.com/rocket-pool/smartnode/shared/utils/log"
	"github.com/urfave/cli/v2"
)

const (
	version    string = "0.1.0"
	colorReset string = "\033[0m"
	colorRed   string = "\033[31m"
)

func main() {

	logger := log.NewColorLogger(color.FgHiWhite)
	errorLogger := log.NewColorLogger(color.FgRed)

	// Initialise application
	app := cli.NewApp()

	// Set application info
	app.Name = "odaotool"
	app.Usage = "A testing tool to run Oracle DAO duties by anyone (without submitting them, of course)."
	app.Version = version
	app.Authors = []*cli.Author{
		{
			Name:  "Joe Clapis",
			Email: "joe@rocketpool.net",
		},
	}
	app.Copyright = "(c) 2023 Rocket Pool Pty Ltd"

	// Set application flags
	app.Flags = []cli.Flag{
		&cli.StringFlag{
			Name:    "ec-endpoint",
			Aliases: []string{"e"},
			Usage:   "The URL of the Execution Client's JSON-RPC API. Note that for past interval generation, this must be an Archive EC.",
			Value:   "http://localhost:8545",
		},
		&cli.StringFlag{
			Name:    "bn-endpoint",
			Aliases: []string{"b"},
			Usage:   "The URL of the Beacon Node's REST API. Note that for past interval generation, this must have Archive capability (ability to replay arbitrary historical states).",
			Value:   "http://localhost:5052",
		},
		&cli.Uint64Flag{
			Name:    "target-block",
			Aliases: []string{"t"},
			Usage:   "(Optional) the EL block to target for duties (default is the chain head if this is omitted)",
			Value:   0,
		},
	}

	// Set commands
	app.Commands = append(app.Commands, &cli.Command{
		Name:      "submit-rpl-price",
		Aliases:   []string{"p"},
		Usage:     "Simulate submitting the RPL price",
		UsageText: "odaotool submit-rpl-price",
		Action: func(c *cli.Context) error {

			submitRplPrice, err := newSubmitRplPrice(c, logger, errorLogger)
			if err != nil {
				return err
			}

			return submitRplPrice.run()

		},
	},
		&cli.Command{
			Name:      "submit-network-balances",
			Aliases:   []string{"b"},
			Usage:     "Simulate submitting the network balances",
			UsageText: "odaotool submit-network-balances",
			Action: func(c *cli.Context) error {

				submitNetworkBalances, err := newSubmitNetworkBalances(c, logger, errorLogger)
				if err != nil {
					return err
				}

				return submitNetworkBalances.run()

			},
		},
	)

	// Allow lots of simultaneous connections
	http.DefaultTransport.(*http.Transport).MaxIdleConnsPerHost = 200

	// Run application
	fmt.Println("")
	err := app.Run(os.Args)
	if err != nil {
		fmt.Printf("%sError during execution: %s%s\n", colorRed, err.Error(), colorReset)
		os.Exit(1)
	}
	fmt.Println("")

}
