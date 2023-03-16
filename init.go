package main

import (
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/rocket-pool/rocketpool-go/rocketpool"
	"github.com/rocket-pool/smartnode/shared/services/beacon"
	"github.com/rocket-pool/smartnode/shared/services/beacon/client"
	"github.com/rocket-pool/smartnode/shared/services/config"
	"github.com/rocket-pool/smartnode/shared/services/state"
	cfgtypes "github.com/rocket-pool/smartnode/shared/types/config"
	"github.com/rocket-pool/smartnode/shared/utils/log"
	"github.com/urfave/cli/v2"
)

// Initialize the common Rocket Pool artifacts necessary for Oracle DAO duty simulation
func initialize(c *cli.Context, log log.ColorLogger) (rocketpool.ExecutionClient, beacon.Client, *rocketpool.RocketPool, *config.RocketPoolConfig, *state.NetworkStateManager, error) {

	// URL acquisiton
	ecUrl := c.String("ec-endpoint")
	if ecUrl == "" {
		return nil, nil, nil, nil, nil, fmt.Errorf("ec-endpoint must be provided")
	}
	bnUrl := c.String("bn-endpoint")
	if ecUrl == "" {
		return nil, nil, nil, nil, nil, fmt.Errorf("bn-endpoint must be provided")
	}

	// Create the EC and BN clients
	ec, err := ethclient.Dial(ecUrl)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("error connecting to the EC: %w", err)
	}
	bc := client.NewStandardHttpClient(bnUrl)

	// Check which network we're on via the BN
	depositContract, err := bc.GetEth2DepositContract()
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("error getting deposit contract from the BN: %w", err)
	}
	var network cfgtypes.Network
	switch depositContract.ChainID {
	case 1:
		network = cfgtypes.Network_Mainnet
		log.Printlnf("Beacon node is configured for Mainnet.")
	case 5:
		network = cfgtypes.Network_Prater
		log.Printlnf("Beacon node is configured for Prater.")
	case 1337803:
		network = cfgtypes.Network_Zhejiang
		log.Printlnf("Beacon node is configured for Zhejiang.")
	default:
		return nil, nil, nil, nil, nil, fmt.Errorf("your Beacon node is configured for an unknown network with Chain ID [%d]", depositContract.ChainID)
	}

	// Create a new config on the proper network
	cfg := config.NewRocketPoolConfig("", true)
	cfg.Smartnode.Network.Value = network

	// Create the RP wrapper
	storageContract := cfg.Smartnode.GetStorageAddress()
	rp, err := rocketpool.NewRocketPool(ec, common.HexToAddress(storageContract))
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("error creating Rocket Pool wrapper: %w", err)
	}

	// Create the state manager
	mgr, err := state.NewNetworkStateManager(rp, cfg, rp.Client, bc, &log)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	return ec, bc, rp, cfg, mgr, nil

}
