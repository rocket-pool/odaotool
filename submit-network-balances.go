package main

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/rocket-pool/rocketpool-go/rocketpool"
	rptypes "github.com/rocket-pool/rocketpool-go/types"
	"github.com/rocket-pool/rocketpool-go/utils/eth"
	rpstate "github.com/rocket-pool/rocketpool-go/utils/state"
	"github.com/urfave/cli/v2"
	"golang.org/x/sync/errgroup"

	"github.com/rocket-pool/smartnode/shared/services/beacon"
	"github.com/rocket-pool/smartnode/shared/services/config"
	rprewards "github.com/rocket-pool/smartnode/shared/services/rewards"
	"github.com/rocket-pool/smartnode/shared/services/state"
	"github.com/rocket-pool/smartnode/shared/utils/eth1"
	"github.com/rocket-pool/smartnode/shared/utils/log"
)

// Submit network balances task
type submitNetworkBalances struct {
	c      *cli.Context
	log    log.ColorLogger
	errLog log.ColorLogger
	cfg    *config.RocketPoolConfig
	ec     rocketpool.ExecutionClient
	rp     *rocketpool.RocketPool
	bc     beacon.Client
	mgr    *state.NetworkStateManager
}

// Network balance info
type networkBalances struct {
	Block                 uint64
	DepositPool           *big.Int
	MinipoolsTotal        *big.Int
	MinipoolsStaking      *big.Int
	DistributorShareTotal *big.Int
	SmoothingPoolShare    *big.Int
	RETHContract          *big.Int
	RETHSupply            *big.Int
	NodeCreditBalance     *big.Int
}
type minipoolBalanceDetails struct {
	IsStaking   bool
	UserBalance *big.Int
}

// Create submit network balances task
func newSubmitNetworkBalances(c *cli.Context, logger log.ColorLogger, errorLogger log.ColorLogger) (*submitNetworkBalances, error) {

	ec, bc, rp, cfg, mgr, err := initialize(c, logger)
	if err != nil {
		return nil, fmt.Errorf("error initializing RP artifacts: %w", err)
	}

	// Return task
	return &submitNetworkBalances{
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

// Submit network balances
func (t *submitNetworkBalances) run() error {

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

	// Check balance submission
	if !state.NetworkDetails.SubmitBalancesEnabled {
		t.log.Println("Balance submissions are currently disabled.")
		return nil
	}

	// Get block to submit balances for
	//blockNumberBig := state.NetworkDetails.LatestReportableBalancesBlock
	blockNumber := state.ElBlockNumber
	blockNumberBig := big.NewInt(int64(blockNumber))
	t.log.Printlnf("Calculating network balances for block %d...", blockNumber)

	// Get network balances at block
	header, err := t.ec.HeaderByNumber(context.Background(), blockNumberBig)
	blockTime := time.Unix(int64(header.Time), 0)
	balances, err := t.getNetworkBalances(header, blockNumberBig, state.BeaconSlotNumber, blockTime, state.IsAtlasDeployed)
	if err != nil {
		t.errLog.Println(err.Error())
		t.errLog.Println("*** Balance report failed. ***")
		return nil
	}

	// Log
	t.log.Printlnf("Deposit pool balance: %s wei", balances.DepositPool.String())
	t.log.Printlnf("Node credit balance: %s wei", balances.NodeCreditBalance.String())
	t.log.Printlnf("Total minipool user balance: %s wei", balances.MinipoolsTotal.String())
	t.log.Printlnf("Staking minipool user balance: %s wei", balances.MinipoolsStaking.String())
	t.log.Printlnf("Fee distributor user balance: %s wei", balances.DistributorShareTotal.String())
	t.log.Printlnf("Smoothing pool user balance: %s wei", balances.SmoothingPoolShare.String())
	t.log.Printlnf("rETH contract balance: %s wei", balances.RETHContract.String())
	t.log.Printlnf("rETH token supply: %s wei", balances.RETHSupply.String())

	// Calculate total ETH balance
	totalEth := big.NewInt(0)
	totalEth.Sub(totalEth, balances.NodeCreditBalance)
	totalEth.Add(totalEth, balances.DepositPool)
	totalEth.Add(totalEth, balances.MinipoolsTotal)
	totalEth.Add(totalEth, balances.RETHContract)
	totalEth.Add(totalEth, balances.DistributorShareTotal)
	totalEth.Add(totalEth, balances.SmoothingPoolShare)

	ratio := eth.WeiToEth(totalEth) / eth.WeiToEth(balances.RETHSupply)
	t.log.Printlnf("Total ETH = %s\n", totalEth)
	t.log.Printlnf("Calculated ratio = %.6f\n", ratio)

	// Log and return
	t.log.Println("Balance report complete.")

	// Return
	return nil

}

// Prints a message to the log
func (t *submitNetworkBalances) printMessage(message string) {
	t.log.Println(message)
}

// Get the network balances at a specific block
func (t *submitNetworkBalances) getNetworkBalances(elBlockHeader *types.Header, elBlock *big.Int, beaconBlock uint64, slotTime time.Time, isAtlasDeployed bool) (networkBalances, error) {

	// Get a client with the block number available
	client, err := eth1.GetBestApiClient(t.rp, t.cfg, t.printMessage, elBlock)
	if err != nil {
		return networkBalances{}, err
	}

	// Create a new state gen manager
	mgr, err := state.NewNetworkStateManager(client, t.cfg, client.Client, t.bc, &t.log)
	if err != nil {
		return networkBalances{}, fmt.Errorf("error creating network state manager for EL block %s, Beacon slot %d: %w", elBlock, beaconBlock, err)
	}

	// Create a new state for the target block
	state, err := mgr.GetStateForSlot(beaconBlock)
	if err != nil {
		return networkBalances{}, fmt.Errorf("couldn't get network state for EL block %s, Beacon slot %d: %w", elBlock, beaconBlock, err)
	}

	// Data
	var wg errgroup.Group
	var depositPoolBalance *big.Int
	var mpBalanceDetails []minipoolBalanceDetails
	var distributorShares []*big.Int
	var smoothingPoolShare *big.Int
	rethContractBalance := state.NetworkDetails.RETHBalance
	rethTotalSupply := state.NetworkDetails.TotalRETHSupply

	// Get deposit pool balance
	if isAtlasDeployed {
		depositPoolBalance = state.NetworkDetails.DepositPoolUserBalance
	} else {
		depositPoolBalance = state.NetworkDetails.DepositPoolBalance
	}

	// Get minipool balance details
	wg.Go(func() error {
		mpBalanceDetails = make([]minipoolBalanceDetails, len(state.MinipoolDetails))
		for i, mpd := range state.MinipoolDetails {
			mpBalanceDetails[i] = t.getMinipoolBalanceDetails(&mpd, state, t.cfg)
		}
		return nil
	})

	// Get distributor balance details
	wg.Go(func() error {
		distributorShares = make([]*big.Int, len(state.NodeDetails))
		for i, node := range state.NodeDetails {
			distributorShares[i] = node.DistributorBalanceUserETH // Uses the go-lib based off-chain calculation method instead of the contract method
		}

		return nil
	})

	// Get the smoothing pool user share
	wg.Go(func() error {

		// Get the current interval
		currentIndex := state.NetworkDetails.RewardIndex

		// Get the start time for the current interval, and how long an interval is supposed to take
		startTime := state.NetworkDetails.IntervalStart
		intervalTime := state.NetworkDetails.IntervalDuration

		timeSinceStart := slotTime.Sub(startTime)
		intervalsPassed := timeSinceStart / intervalTime
		endTime := slotTime

		// Approximate the staker's share of the smoothing pool balance
		treegen, err := rprewards.NewTreeGenerator(t.log, "[Balances]", client, t.cfg, t.bc, currentIndex, startTime, endTime, beaconBlock, elBlockHeader, uint64(intervalsPassed), state)
		if err != nil {
			return fmt.Errorf("error creating merkle tree generator to approximate share of smoothing pool: %w", err)
		}
		smoothingPoolShare, err = treegen.ApproximateStakerShareOfSmoothingPool()
		if err != nil {
			return fmt.Errorf("error getting approximate share of smoothing pool: %w", err)
		}

		return nil

	})

	// Wait for data
	if err := wg.Wait(); err != nil {
		return networkBalances{}, err
	}

	// Balances
	balances := networkBalances{
		Block:                 elBlockHeader.Number.Uint64(),
		DepositPool:           depositPoolBalance,
		MinipoolsTotal:        big.NewInt(0),
		MinipoolsStaking:      big.NewInt(0),
		DistributorShareTotal: big.NewInt(0),
		SmoothingPoolShare:    smoothingPoolShare,
		RETHContract:          rethContractBalance,
		RETHSupply:            rethTotalSupply,
		NodeCreditBalance:     big.NewInt(0),
	}

	// Add minipool balances
	for _, mp := range mpBalanceDetails {
		balances.MinipoolsTotal.Add(balances.MinipoolsTotal, mp.UserBalance)
		if mp.IsStaking {
			balances.MinipoolsStaking.Add(balances.MinipoolsStaking, mp.UserBalance)
		}
	}

	// Add node credits
	if state.IsAtlasDeployed {
		for _, node := range state.NodeDetails {
			balances.NodeCreditBalance.Add(balances.NodeCreditBalance, node.DepositCreditBalance)
		}
	}

	// Add distributor shares
	for _, share := range distributorShares {
		balances.DistributorShareTotal.Add(balances.DistributorShareTotal, share)
	}

	// Return
	return balances, nil

}

// Get minipool balance details
func (t *submitNetworkBalances) getMinipoolBalanceDetails(mpd *rpstate.NativeMinipoolDetails, state *state.NetworkState, cfg *config.RocketPoolConfig) minipoolBalanceDetails {

	status := mpd.Status
	userDepositBalance := mpd.UserDepositBalance
	mpType := mpd.DepositType
	validator := state.ValidatorDetails[mpd.Pubkey]

	blockEpoch := state.BeaconSlotNumber / state.BeaconConfig.SlotsPerEpoch

	// Ignore vacant minipools
	if mpd.IsVacant {
		return minipoolBalanceDetails{
			UserBalance: big.NewInt(0),
		}
	}

	// Dissolved minipools don't contribute to rETH
	if status == rptypes.Dissolved {
		return minipoolBalanceDetails{
			UserBalance: big.NewInt(0),
		}
	}

	// Use user deposit balance if initialized or prelaunch
	if status == rptypes.Initialized || status == rptypes.Prelaunch {
		return minipoolBalanceDetails{
			UserBalance: userDepositBalance,
		}
	}

	// "Broken" LEBs with the Redstone delegates report their total balance minus their node deposit balance
	if mpd.DepositType == rptypes.Variable && mpd.Version == 2 {
		brokenBalance := big.NewInt(0).Set(mpd.Balance)
		brokenBalance.Add(brokenBalance, eth.GweiToWei(float64(validator.Balance)))
		brokenBalance.Sub(brokenBalance, mpd.NodeRefundBalance)
		brokenBalance.Sub(brokenBalance, mpd.NodeDepositBalance)
		return minipoolBalanceDetails{
			IsStaking:   (validator.Exists && validator.ActivationEpoch < blockEpoch && validator.ExitEpoch > blockEpoch),
			UserBalance: brokenBalance,
		}
	}

	// Use user deposit balance if validator not yet active on beacon chain at block
	if !validator.Exists || validator.ActivationEpoch >= blockEpoch {
		return minipoolBalanceDetails{
			UserBalance: userDepositBalance,
		}
	}

	// Here userBalance is CalculateUserShare(beaconBalance + minipoolBalance - refund)
	userBalance := mpd.UserShareOfBalanceIncludingBeacon
	if userDepositBalance.Cmp(big.NewInt(0)) == 0 && mpType == rptypes.Full {
		return minipoolBalanceDetails{
			IsStaking:   (validator.ExitEpoch > blockEpoch),
			UserBalance: big.NewInt(0).Sub(userBalance, eth.EthToWei(16)), // Remove 16 ETH from the user balance for full minipools in the refund queue
		}
	} else {
		return minipoolBalanceDetails{
			IsStaking:   (validator.ExitEpoch > blockEpoch),
			UserBalance: userBalance,
		}
	}

}
