package main

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/rocket-pool/rocketpool-go/rocketpool"
	"github.com/rocket-pool/rocketpool-go/utils/eth"
	"github.com/urfave/cli/v2"

	"github.com/rocket-pool/smartnode/shared/services/beacon"
	"github.com/rocket-pool/smartnode/shared/services/config"
	"github.com/rocket-pool/smartnode/shared/services/state"
	"github.com/rocket-pool/smartnode/shared/utils/eth1"
	"github.com/rocket-pool/smartnode/shared/utils/log"
	mathutils "github.com/rocket-pool/smartnode/shared/utils/math"
)

const (
	RplTwapPoolAbi string = `[
		{
		"inputs": [{
			"internalType": "uint32[]",
			"name": "secondsAgos",
			"type": "uint32[]"
		}],
		"name": "observe",
		"outputs": [{
			"internalType": "int56[]",
			"name": "tickCumulatives",
			"type": "int56[]"
		}, {
			"internalType": "uint160[]",
			"name": "secondsPerLiquidityCumulativeX128s",
			"type": "uint160[]"
		}],
		"stateMutability": "view",
		"type": "function"
		}
	]`
)

// Settings
const (
	twapNumberOfSeconds uint32 = 60 * 60 * 12 // 12 hours
)

type poolObserveResponse struct {
	TickCumulatives                    []*big.Int `abi:"tickCumulatives"`
	SecondsPerLiquidityCumulativeX128s []*big.Int `abi:"secondsPerLiquidityCumulativeX128s"`
}

// Submit RPL price task
type submitRplPrice struct {
	c      *cli.Context
	log    log.ColorLogger
	errLog log.ColorLogger
	cfg    *config.RocketPoolConfig
	ec     rocketpool.ExecutionClient
	rp     *rocketpool.RocketPool
	bc     beacon.Client
	mgr    *state.NetworkStateManager
}

// Create submit RPL price task
func newSubmitRplPrice(c *cli.Context, logger log.ColorLogger, errorLogger log.ColorLogger) (*submitRplPrice, error) {

	ec, bc, rp, cfg, mgr, err := initialize(c, logger)
	if err != nil {
		return nil, fmt.Errorf("error initializing RP artifacts: %w", err)
	}

	// Return task
	return &submitRplPrice{
		c:      c,
		log:    logger,
		errLog: errorLogger,
		cfg:    cfg,
		ec:     ec,
		rp:     rp,
		bc:     bc,
		mgr:    mgr,
	}, nil

}

// Submit RPL price
func (t *submitRplPrice) run() error {

	var state *state.NetworkState
	if t.c.IsSet("target-block") {
		// Get the time of the block
		blockNumber := t.c.Uint64("target-block")
		header, err := t.ec.HeaderByNumber(context.Background(), big.NewInt(0).SetUint64(blockNumber))
		if err != nil {
			return err
		}
		blockTime := time.Unix(int64(header.Time), 0)

		// Get the Beacon block corresponding to this time
		eth2Config, err := t.bc.GetEth2Config()
		if err != nil {
			return fmt.Errorf("error getting beacon config: %w", err)
		}
		genesisTime := time.Unix(int64(eth2Config.GenesisTime), 0)
		timeSinceGenesis := blockTime.Sub(genesisTime)
		slotNumber := uint64(timeSinceGenesis.Seconds()) / eth2Config.SecondsPerSlot
		state, err = t.mgr.GetStateForSlot(slotNumber)
		if err != nil {
			return fmt.Errorf("error getting state for EL block %d, CL slot %d: %w", blockNumber, slotNumber, err)
		}
	} else {
		t.log.Printlnf("Target block not set, getting the state of the chain head.")
		var err error
		state, err = t.mgr.GetHeadState()
		if err != nil {
			return fmt.Errorf("error getting network state for head slot: %w", err)
		}
	}

	// Check if submission is enabled
	if !state.NetworkDetails.SubmitPricesEnabled {
		t.log.Println("Price submissions are currently disabled.")
		return nil
	}

	// Get block to submit price for
	//blockNumber := state.NetworkDetails.LatestReportablePricesBlock
	blockNumber := state.ElBlockNumber
	t.log.Printlnf("Getting RPL price for block %d...", blockNumber)

	// Get RPL price at block
	rplPrice, err := t.getRplTwap(blockNumber)
	if err != nil {
		t.errLog.Println(err.Error())
		t.errLog.Println("*** Price report failed. ***")
		return nil
	}

	// Log
	t.log.Printlnf("RPL price: %.6f ETH", mathutils.RoundDown(eth.WeiToEth(rplPrice), 6))

	// Log and return
	t.log.Println("Price report complete.")

	// Return
	return nil

}

// Get RPL price via TWAP at block
func (t *submitRplPrice) getRplTwap(blockNumber uint64) (*big.Int, error) {

	// Initialize call options
	opts := &bind.CallOpts{
		BlockNumber: big.NewInt(int64(blockNumber)),
	}

	poolAddress := t.cfg.Smartnode.GetRplTwapPoolAddress()
	if poolAddress == "" {
		return nil, fmt.Errorf("RPL TWAP pool contract not deployed on this network")
	}

	// Get a client with the block number available
	client, err := eth1.GetBestApiClient(t.rp, t.cfg, t.printMessage, opts.BlockNumber)
	if err != nil {
		return nil, err
	}

	// Construct the pool contract instance
	parsed, err := abi.JSON(strings.NewReader(RplTwapPoolAbi))
	if err != nil {
		return nil, fmt.Errorf("error decoding RPL TWAP pool ABI: %w", err)
	}
	addr := common.HexToAddress(poolAddress)
	t.log.Printlnf("TWAP Address: %s", addr.Hex())
	poolContract := bind.NewBoundContract(addr, parsed, client.Client, client.Client, client.Client)
	pool := rocketpool.Contract{
		Contract: poolContract,
		Address:  &addr,
		ABI:      &parsed,
		Client:   client.Client,
	}

	// Get RPL price
	response := poolObserveResponse{}
	interval := twapNumberOfSeconds
	args := []uint32{interval, 0}
	t.log.Printlnf("Number of seconds in interval: %d", interval)

	err = pool.Call(opts, &response, "observe", args)
	if err != nil {
		return nil, fmt.Errorf("could not get RPL price at block %d: %w", blockNumber, err)
	}

	tick := big.NewInt(0).Sub(response.TickCumulatives[1], response.TickCumulatives[0])
	tick.Div(tick, big.NewInt(int64(interval))) // tick = (cumulative[1] - cumulative[0]) / interval

	base := eth.EthToWei(1.0001) // 1.0001e18
	one := eth.EthToWei(1)       // 1e18

	numerator := big.NewInt(0).Exp(base, tick, nil) // 1.0001e18 ^ tick
	numerator.Mul(numerator, one)

	denominator := big.NewInt(0).Exp(one, tick, nil) // 1e18 ^ tick
	denominator.Div(numerator, denominator)          // denominator = (1.0001e18^tick / 1e18^tick)

	numerator.Mul(one, one)                               // 1e18 ^ 2
	rplPrice := big.NewInt(0).Div(numerator, denominator) // 1e18 ^ 2 / (1.0001e18^tick * 1e18 / 1e18^tick)

	// Return
	return rplPrice, nil

}

func (t *submitRplPrice) printMessage(message string) {
	t.log.Println(message)
}
