package consensus

import (
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/NebulousLabs/Sia/modules"
	"github.com/NebulousLabs/Sia/types"
)

// TestDoSBlockHandling checks that saved bad blocks are correctly ignored.
func TestDoSBlockHandling(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	cst, err := createConsensusSetTester("TestDoSBlockHandling")
	if err != nil {
		t.Fatal(err)
	}

	// Mine a DoS block and submit it to the state, expect a normal error.
	// Create a transaction that is funded but the funds are never spent. This
	// transaction is invalid in a way that triggers the DoS block detection.
	id, err := cst.wallet.RegisterTransaction(types.Transaction{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = cst.wallet.FundTransaction(id, types.NewCurrency64(50))
	if err != nil {
		t.Fatal(err)
	}
	cst.tpUpdateWait()
	txn, err := cst.wallet.SignTransaction(id, true) // true indicates that the whole transaction should be signed.
	if err != nil {
		t.Fatal(err)
	}

	// Get a block, insert the transaction, and submit the block.
	block, _, target := cst.miner.BlockForWork()
	block.Transactions = append(block.Transactions, txn)
	dosBlock, _ := cst.miner.SolveBlock(block, target)
	err = cst.cs.AcceptBlock(dosBlock)
	if err != ErrSiacoinInputOutputMismatch {
		t.Fatal("expecting invalid signature err: " + err.Error())
	}

	// Submit the same DoS block to the state again, expect ErrDoSBlock.
	err = cst.cs.AcceptBlock(dosBlock)
	if err != ErrDoSBlock {
		t.Fatal("expecting bad block err: " + err.Error())
	}
}

// testBlockKnownHandling submits known blocks to the consensus set.
func (cst *consensusSetTester) testBlockKnownHandling() error {
	// Get a block destined to be stale.
	block, _, target := cst.miner.BlockForWork()
	staleBlock, _ := cst.miner.SolveBlock(block, target)

	// Add two new blocks to the consensus set to block the stale block.
	block1, _ := cst.miner.FindBlock()
	err := cst.cs.AcceptBlock(block1)
	if err != nil {
		return err
	}
	cst.csUpdateWait()
	block2, _ := cst.miner.FindBlock()
	err = cst.cs.AcceptBlock(block2)
	if err != nil {
		return err
	}
	cst.csUpdateWait()

	// Submit the stale block.
	err = cst.cs.acceptBlock(staleBlock)
	if err != nil && err != modules.ErrNonExtendingBlock {
		return err
	}

	// Submit block1 and block2 again, looking for a 'BlockKnown' error.
	err = cst.cs.acceptBlock(block1)
	if err != ErrBlockKnown {
		return errors.New("expecting known block err: " + err.Error())
	}
	err = cst.cs.acceptBlock(block2)
	if err != ErrBlockKnown {
		return errors.New("expecting known block err: " + err.Error())
	}
	err = cst.cs.acceptBlock(staleBlock)
	if err != ErrBlockKnown {
		return errors.New("expecting known block err: " + err.Error())
	}

	// Try the genesis block edge case.
	genesisBlock := cst.cs.blockMap[cst.cs.currentPath[0]].block
	err = cst.cs.acceptBlock(genesisBlock)
	if err != ErrBlockKnown {
		return errors.New("expecting known block err: " + err.Error())
	}
	return nil
}

// TestBlockKnownHandling creates a new consensus set tester and uses it to
// call testBlockKnownHandling.
func TestBlockKnownHandling(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	cst, err := createConsensusSetTester("TestBlockKnownHandling")
	if err != nil {
		t.Fatal(err)
	}
	err = cst.testBlockKnownHandling()
	if err != nil {
		t.Error(err)
	}
}

// TestOrphanHandling passes an orphan block to the consensus set.
func TestOrphanHandling(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	cst, err := createConsensusSetTester("TestOrphanHandling")
	if err != nil {
		t.Fatal(err)
	}

	// The empty block is an orphan.
	orphan := types.Block{}
	err = cst.cs.acceptBlock(orphan)
	if err != ErrOrphan {
		t.Error("expecting ErrOrphan:", err)
	}
	err = cst.cs.acceptBlock(orphan)
	if err != ErrOrphan {
		t.Error("expecting ErrOrphan:", err)
	}
}

// TestMissedTarget submits a block that does not meet the required target.
func TestMissedTarget(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	cst, err := createConsensusSetTester("TestMissedTarget")
	if err != nil {
		t.Fatal(err)
	}

	// Mine a block that doesn't meet the target.
	block, _, target := cst.miner.BlockForWork()
	for block.CheckTarget(target) && block.Nonce[0] != 255 {
		block.Nonce[0]++
	}
	if block.CheckTarget(target) {
		t.Fatal("unable to find a failing target (lol)")
	}
	err = cst.cs.acceptBlock(block)
	if err != ErrMissedTarget {
		t.Error("expecting ErrMissedTarget:", err)
	}
}

// testLargeBlock creates a block that is too large to be accepted by the state
// and checks that it actually gets rejected.
func TestLargeBlock(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	cst, err := createConsensusSetTester("TestLargeBlock")
	if err != nil {
		t.Fatal(err)
	}

	// Create a transaction that puts the block over the size limit.
	bigData := make([]byte, types.BlockSizeLimit)
	txn := types.Transaction{
		ArbitraryData: [][]byte{bigData},
	}

	// Fetch a block and add the transaction, then submit the block.
	block, _, target := cst.miner.BlockForWork()
	block.Transactions = append(block.Transactions, txn)
	solvedBlock, _ := cst.miner.SolveBlock(block, target)
	err = cst.cs.acceptBlock(solvedBlock)
	if err != ErrLargeBlock {
		t.Error(err)
	}
}

// TestEarlyBlockTimestampHandling checks that blocks with early timestamps are
// handled appropriately.
func TestEarlyBlockTimestampHandling(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	cst, err := createConsensusSetTester("TestBlockTimestampHandling")
	if err != nil {
		t.Fatal(err)
	}

	// Create a block with a too early timestamp - block should be rejected
	// outright.
	block, _, target := cst.miner.BlockForWork()
	earliestTimestamp := cst.cs.blockMap[block.ParentID].earliestChildTimestamp()
	block.Timestamp = earliestTimestamp - 1
	earlyBlock, _ := cst.miner.SolveBlock(block, target)
	err = cst.cs.acceptBlock(earlyBlock)
	if err != ErrEarlyTimestamp {
		t.Error("expecting ErrEarlyTimestamp:", err.Error())
	}
}

// TestExtremeFutureTimestampHandling checks that blocks with extreme future
// timestamps handled correclty.
func TestExtremeFutureTimestampHandling(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	cst, err := createConsensusSetTester("TestExtremeFutureTimestampHandling")
	if err != nil {
		t.Fatal(err)
	}

	// Submit a block with a timestamp in the extreme future.
	block, _, target := cst.miner.BlockForWork()
	block.Timestamp = types.CurrentTimestamp() + 2 + types.ExtremeFutureThreshold
	solvedBlock, _ := cst.miner.SolveBlock(block, target)
	err = cst.cs.acceptBlock(solvedBlock)
	if err != ErrExtremeFutureTimestamp {
		t.Error("Expecting ErrExtremeFutureTimestamp", err)
	}

	// Check that after waiting until the block is no longer in the future, the
	// block still has not been added to the consensus set (prove that the
	// block was correctly discarded).
	time.Sleep(time.Second * time.Duration(3+types.ExtremeFutureThreshold))
	lockID := cst.cs.mu.RLock()
	defer cst.cs.mu.RUnlock(lockID)
	_, exists := cst.cs.blockMap[solvedBlock.ID()]
	if exists {
		t.Error("extreme future block made it into the consensus set after waiting")
	}
}

// TestMinerPayoutHandling checks that blocks with incorrect payouts are
// rejected.
func TestMinerPayoutHandling(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	cst, err := createConsensusSetTester("TestMinerPayoutHandling")
	if err != nil {
		t.Fatal(err)
	}

	// Create a block with the wrong miner payout structure - testing can be
	// light here because there is heavier testing in the 'types' package,
	// where the logic is defined.
	block, _, target := cst.miner.BlockForWork()
	block.MinerPayouts = append(block.MinerPayouts, types.SiacoinOutput{Value: types.NewCurrency64(1)})
	solvedBlock, _ := cst.miner.SolveBlock(block, target)
	err = cst.cs.acceptBlock(solvedBlock)
	if err != ErrBadMinerPayouts {
		t.Error(err)
	}
}

// testFutureTimestampHandling checks that blocks in the future (but not
// extreme future) are handled correctly.
func (cst *consensusSetTester) testFutureTimestampHandling() error {
	// Submit a block with a timestamp in the future, but not the extreme
	// future.
	block, _, target := cst.miner.BlockForWork()
	block.Timestamp = types.CurrentTimestamp() + 2 + types.FutureThreshold
	solvedBlock, _ := cst.miner.SolveBlock(block, target)
	err := cst.cs.acceptBlock(solvedBlock)
	if err != ErrFutureTimestamp {
		return errors.New("Expecting ErrExtremeFutureTimestamp: " + err.Error())
	}

	// Check that after waiting until the block is no longer too far in the
	// future, the block gets added to the consensus set.
	time.Sleep(time.Second * 3) // 3 seconds, as the block was originally 2 seconds too far into the future.
	lockID := cst.cs.mu.RLock()
	defer cst.cs.mu.RUnlock(lockID)
	_, exists := cst.cs.blockMap[solvedBlock.ID()]
	if !exists {
		return errors.New("future block was not added to the consensus set after waiting the appropriate amount of time.")
	}
	return nil
}

// TestFutureTimestampHandling creates a consensus set tester and uses it to
// call testFutureTimestampHandling.
func TestFutureTimestampHandling(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	cst, err := createConsensusSetTester("TestFutureTimestampHandling")
	if err != nil {
		t.Fatal(err)
	}
	err = cst.testFutureTimestampHandling()
	if err != nil {
		t.Error(err)
	}
}

// TestInconsistentCheck submits a block on a consensus set that is
// inconsistent, attempting to trigger a panic.
func TestInconsistentCheck(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	cst, err := createConsensusSetTester("TestInconsistentCheck")
	if err != nil {
		t.Fatal(err)
	}

	// Corrupt the consensus set.
	cst.cs.siafundPool = cst.cs.siafundPool.Add(types.NewCurrency64(1))

	// Mine and submit a block, triggering the inconsistency check.
	defer func() {
		r := recover()
		if r != errSiacoinMiscount {
			t.Error("expecting errSiacoinMiscount, got:", r)
		}
	}()
	block, _ := cst.miner.FindBlock()
	_ = cst.cs.AcceptBlock(block)
}

// testSimpleBlock mines a simple block (no transactions except those
// automatically added by the miner) and adds it to the consnesus set.
func (cst *consensusSetTester) testSimpleBlock() error {
	// Get the starting hash of the consenesus set.
	initialCSSum := cst.cs.consensusSetHash()

	// Mine and submit a block
	block, _ := cst.miner.FindBlock()
	err := cst.cs.AcceptBlock(block)
	if err != nil {
		return err
	}
	cst.csUpdateWait()

	// Get the ending hash of the consensus set.
	resultingCSSum := cst.cs.consensusSetHash()
	if initialCSSum == resultingCSSum {
		return errors.New("state hash is unchanged after mining a block")
	}

	// Check that the current path has updated as expected.
	newNode := cst.cs.currentBlockNode()
	if cst.cs.CurrentBlock().ID() != block.ID() {
		return errors.New("the state's current block is not reporting as the recently mined block.")
	}
	// Check that the current path has updated correctly.
	if block.ID() != cst.cs.currentPath[newNode.height] {
		return errors.New("the state's current path didn't update correctly after accepting a new block")
	}

	// Revert the block that was just added to the consensus set and check for
	// parity with the original state of consensus.
	_, _, err = cst.cs.forkBlockchain(newNode.parent)
	if err != nil {
		return err
	}
	if cst.cs.consensusSetHash() != initialCSSum {
		return errors.New("adding and reverting a block changed the consensus set")
	}
	// Re-add the block and check for parity with the first time it was added.
	// This test is useful because a different codepath is followed if the
	// diffs have already been generated.
	_, _, err = cst.cs.forkBlockchain(newNode)
	if cst.cs.consensusSetHash() != resultingCSSum {
		return errors.New("adding, reverting, and reading a block was inconsistent with just adding the block")
	}
	return nil
}

// TestSimpleBlock creates a consensus set tester and uses it to call
// testSimpleBlock.
func TestSimpleBlock(t *testing.T) {
	cst, err := createConsensusSetTester("TestSimpleBlock")
	if err != nil {
		t.Fatal(err)
	}
	err = cst.testSimpleBlock()
	if err != nil {
		t.Error(err)
	}
}

// testSpendSiacoinsBlock mines a block with a transaction spending siacoins
// and adds it to the consensus set.
func (cst *consensusSetTester) testSpendSiacoinsBlock() error {
	// Create a random destination address for the output in the transaction.
	var destAddr types.UnlockHash
	_, err := rand.Read(destAddr[:])
	if err != nil {
		return err
	}

	// Create a block containing a transaction with a valid siacoin output.
	txnValue := types.NewCurrency64(1200)
	id, err := cst.wallet.RegisterTransaction(types.Transaction{})
	if err != nil {
		return err
	}
	_, err = cst.wallet.FundTransaction(id, txnValue)
	if err != nil {
		return err
	}
	cst.tpUpdateWait()
	_, outputIndex, err := cst.wallet.AddSiacoinOutput(id, types.SiacoinOutput{Value: txnValue, UnlockHash: destAddr})
	if err != nil {
		return err
	}
	txn, err := cst.wallet.SignTransaction(id, true)
	if err != nil {
		return err
	}
	err = cst.tpool.AcceptTransaction(txn)
	if err != nil {
		return err
	}
	cst.tpUpdateWait()
	outputID := txn.SiacoinOutputID(int(outputIndex))

	// Mine and apply the block to the consensus set.
	block, _ := cst.miner.FindBlock()
	err = cst.cs.AcceptBlock(block)
	if err != nil {
		return err
	}
	cst.csUpdateWait()

	// Find the destAddr among the outputs.
	var found bool
	for id, output := range cst.cs.siacoinOutputs {
		if id == outputID {
			if found {
				return errors.New("output found twice")
			}
			if output.Value.Cmp(txnValue) != 0 {
				return errors.New("output has wrong value")
			}
			found = true
		}
	}
	if !found {
		return errors.New("could not find created siacoin output")
	}
	return nil
}

// TestSpendSiacoinsBlock creates a consensus set and uses it to call
// testSpendSiacoinsBlock.
func TestSpendSiacoinsBlock(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	cst, err := createConsensusSetTester("TestSpendSiacoinsBlock")
	if err != nil {
		t.Fatal(err)
	}
	err = cst.testSpendSiacoinsBlock()
	if err != nil {
		t.Error(err)
	}
}
