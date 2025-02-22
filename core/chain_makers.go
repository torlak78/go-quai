// Copyright 2015 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package core

import (
	"errors"
	"fmt"
	"log"
	"math/big"
	"sync"

	"github.com/spruce-solutions/go-quai/common"
	"github.com/spruce-solutions/go-quai/consensus"
	"github.com/spruce-solutions/go-quai/consensus/misc"
	"github.com/spruce-solutions/go-quai/core/state"
	"github.com/spruce-solutions/go-quai/core/types"
	"github.com/spruce-solutions/go-quai/core/vm"
	"github.com/spruce-solutions/go-quai/ethdb"
	"github.com/spruce-solutions/go-quai/params"
	"github.com/spruce-solutions/go-quai/trie"
)

// BlockGen creates blocks for testing.
// See GenerateChain for a detailed explanation.
type BlockGen struct {
	i       int
	parent  *types.Block
	chain   []*types.Block
	header  *types.Header
	statedb *state.StateDB

	gasPool  *GasPool
	txs      []*types.Transaction
	receipts []*types.Receipt
	uncles   []*types.Header

	config *params.ChainConfig
	engine consensus.Engine
}

// SetCoinbase sets the coinbase of the generated block.
// It can be called at most once.
func (b *BlockGen) SetCoinbase(addr common.Address) {
	if b.gasPool != nil {
		if len(b.txs) > 0 {
			panic("coinbase must be set before adding transactions")
		}
		panic("coinbase can only be set once")
	}
	b.header.Coinbase[types.QuaiNetworkContext] = addr
	b.gasPool = new(GasPool).AddGas(b.header.GasLimit[types.QuaiNetworkContext])
}

// SetExtra sets the extra data field of the generated block.
func (b *BlockGen) SetExtra(data []byte) {
	b.header.Extra[types.QuaiNetworkContext] = data
}

// SetNonce sets the nonce field of the generated block.
func (b *BlockGen) SetNonce(nonce types.BlockNonce) {
	b.header.Nonce = nonce
}

// SetDifficulty sets the difficulty field of the generated block. This method is
// useful for Clique tests where the difficulty does not depend on time. For the
// ethash tests, please use OffsetTime, which implicitly recalculates the diff.
func (b *BlockGen) SetDifficulty(diff *big.Int) {
	b.header.Difficulty[types.QuaiNetworkContext] = diff
}

// AddTx adds a transaction to the generated block. If no coinbase has
// been set, the block's coinbase is set to the zero address.
//
// AddTx panics if the transaction cannot be executed. In addition to
// the protocol-imposed limitations (gas limit, etc.), there are some
// further limitations on the content of transactions that can be
// added. Notably, contract code relying on the BLOCKHASH instruction
// will panic during execution.
func (b *BlockGen) AddTx(tx *types.Transaction) {
	b.AddTxWithChain(nil, tx)
}

// AddTxWithChain adds a transaction to the generated block. If no coinbase has
// been set, the block's coinbase is set to the zero address.
//
// AddTxWithChain panics if the transaction cannot be executed. In addition to
// the protocol-imposed limitations (gas limit, etc.), there are some
// further limitations on the content of transactions that can be
// added. If contract code relies on the BLOCKHASH instruction,
// the block in chain will be returned.
func (b *BlockGen) AddTxWithChain(bc *BlockChain, tx *types.Transaction) {
	if b.gasPool == nil {
		b.SetCoinbase(common.Address{})
	}
	b.statedb.Prepare(tx.Hash(), len(b.txs))
	receipt, err := ApplyTransaction(b.config, bc, &b.header.Coinbase[types.QuaiNetworkContext], b.gasPool, b.statedb, b.header, tx, &b.header.GasUsed[types.QuaiNetworkContext], vm.Config{})
	if err != nil {
		panic(err)
	}
	b.txs = append(b.txs, tx)
	b.receipts = append(b.receipts, receipt)
}

// GetBalance returns the balance of the given address at the generated block.
func (b *BlockGen) GetBalance(addr common.Address) *big.Int {
	return b.statedb.GetBalance(addr)
}

// AddUncheckedTx forcefully adds a transaction to the block without any
// validation.
//
// AddUncheckedTx will cause consensus failures when used during real
// chain processing. This is best used in conjunction with raw block insertion.
func (b *BlockGen) AddUncheckedTx(tx *types.Transaction) {
	b.txs = append(b.txs, tx)
}

// Number returns the block number of the block being generated.
func (b *BlockGen) Number() *big.Int {
	return new(big.Int).Set(b.header.Number[types.QuaiNetworkContext])
}

// BaseFee returns the EIP-1559 base fee of the block being generated.
func (b *BlockGen) BaseFee() *big.Int {
	return new(big.Int).Set(b.header.BaseFee[types.QuaiNetworkContext])
}

// AddUncheckedReceipt forcefully adds a receipts to the block without a
// backing transaction.
//
// AddUncheckedReceipt will cause consensus failures when used during real
// chain processing. This is best used in conjunction with raw block insertion.
func (b *BlockGen) AddUncheckedReceipt(receipt *types.Receipt) {
	b.receipts = append(b.receipts, receipt)
}

// TxNonce returns the next valid transaction nonce for the
// account at addr. It panics if the account does not exist.
func (b *BlockGen) TxNonce(addr common.Address) uint64 {
	if !b.statedb.Exist(addr) {
		panic("account does not exist")
	}
	return b.statedb.GetNonce(addr)
}

// AddUncle adds an uncle header to the generated block.
func (b *BlockGen) AddUncle(h *types.Header) {
	b.uncles = append(b.uncles, h)
}

// PrevBlock returns a previously generated block by number. It panics if
// num is greater or equal to the number of the block being generated.
// For index -1, PrevBlock returns the parent block given to GenerateChain.
func (b *BlockGen) PrevBlock(index int) *types.Block {
	if index >= b.i {
		panic(fmt.Errorf("block index %d out of range (%d,%d)", index, -1, b.i))
	}
	if index == -1 {
		return b.parent
	}
	return b.chain[index]
}

// OffsetTime modifies the time instance of a block, implicitly changing its
// associated difficulty. It's useful to test scenarios where forking is not
// tied to chain length directly.
func (b *BlockGen) OffsetTime(seconds int64) {
	b.header.Time += uint64(seconds)
	if b.header.Time <= b.parent.Header().Time {
		panic("block time out of range")
	}
	chainreader := &fakeChainReader{config: b.config}
	b.header.Difficulty[types.QuaiNetworkContext] = b.engine.CalcDifficulty(chainreader, b.header.Time, b.parent.Header(), types.QuaiNetworkContext)
}

// GenerateChain creates a chain of n blocks. The first block's
// parent will be the provided parent. db is used to store
// intermediate states and should contain the parent's state trie.
//
// The generator function is called with a new block generator for
// every block. Any transactions and uncles added to the generator
// become part of the block. If gen is nil, the blocks will be empty
// and their coinbase will be the zero address.
//
// Blocks created by GenerateChain do not contain valid proof of work
// values. Inserting them into BlockChain requires use of FakePow or
// a similar non-validating proof of work implementation.
func GenerateChain(config *params.ChainConfig, parent *types.Block, engine consensus.Engine, db ethdb.Database, n int, gen func(int, *BlockGen)) ([]*types.Block, []types.Receipts) {
	if config == nil {
		config = params.TestChainConfig
	}
	blocks, receipts := make(types.Blocks, n), make([]types.Receipts, n)
	chainreader := &fakeChainReader{config: config}
	genblock := func(i int, parent *types.Block, statedb *state.StateDB) (*types.Block, types.Receipts) {
		b := &BlockGen{i: i, chain: blocks, parent: parent, statedb: statedb, config: config, engine: engine}
		location := []byte{1, 1}
		b.header = makeHeader(chainreader, parent, statedb, b.engine, location)

		// Execute any user modifications to the block
		if gen != nil {
			gen(i, b)
		}
		if b.engine != nil {
			// Finalize and seal the block
			block, _ := b.engine.FinalizeAndAssemble(chainreader, b.header, statedb, b.txs, b.uncles, b.receipts)

			// Write state changes to db
			root, err := statedb.Commit(config.IsEIP158(b.header.Number[types.QuaiNetworkContext]))
			if err != nil {
				panic(fmt.Sprintf("state write error: %v", err))
			}
			if err := statedb.Database().TrieDB().Commit(root, false, nil); err != nil {
				panic(fmt.Sprintf("trie write error: %v", err))
			}
			return block, b.receipts
		}
		return nil, nil
	}
	for i := 0; i < n; i++ {
		statedb, err := state.New(parent.Root(), state.NewDatabase(db), nil)
		if err != nil {
			panic(err)
		}
		block, receipt := genblock(i, parent, statedb)
		blocks[i] = block
		receipts[i] = receipt
		parent = block
	}
	return blocks, receipts
}

type knot struct {
	block *types.Block
}

func GenerateKnot(config *params.ChainConfig, parent *types.Block, engine consensus.Engine, db ethdb.Database, n int, gen func(int, *BlockGen)) ([]*types.Block, []types.Receipts) {
	if config == nil {
		config = params.TestChainConfig
	}
	blocks, receipts := make(types.Blocks, n), make([]types.Receipts, n)
	chainreader := &fakeChainReader{config: config}

	resultLoop := func(i int, k *knot, statedb *state.StateDB, results chan *types.Block, stop chan struct{}, wg *sync.WaitGroup) error {
		for {
			select {
			case block := <-results:
				context, err := engine.GetDifficultyOrder(block.Header())
				if err != nil {
					log.Println("Block mined has an invalid order")
				}

				if context == params.PRIME {
					log.Println("PRIME:", block.Header().Location, block.Header().Number, block.Header().Hash())
				}

				k.block = block

				defer wg.Done()

				return nil
			}
		}
	}

	genblock := func(i int, parent *types.Block, statedb *state.StateDB, location []byte, resultCh chan *types.Block, stopCh chan struct{}) {
		b := &BlockGen{i: i, chain: blocks, parent: parent, statedb: statedb, config: config, engine: engine}
		b.header = makeHeader(chainreader, parent, statedb, b.engine, location)

		// Execute any user modifications to the block
		if gen != nil {
			gen(i, b)
		}
		if b.engine != nil {
			// Finalize and seal the block
			block := types.NewBlock(b.header, nil, nil, nil, trie.NewStackTrie(nil))

			// Mine the block
			if err := engine.Seal(chainreader, block, resultCh, stopCh); err != nil {
				log.Println("Block sealing failed", "err", err)
			}
		}
	}

	knot := &knot{
		block: parent,
	}

	locations := [][]byte{[]byte{1, 1}, []byte{1, 2}, []byte{1, 3}, []byte{2, 1}, []byte{2, 2}, []byte{2, 3}, []byte{3, 1}, []byte{3, 2}, []byte{3, 3}}
	for i := 0; i < len(locations); i++ {
		statedb, err := state.New(parent.Root(), state.NewDatabase(db), nil)
		if err != nil {
			panic(err)
		}
		resultCh := make(chan *types.Block)
		exitCh := make(chan struct{})

		var wg sync.WaitGroup
		wg.Add(1)
		go resultLoop(i, knot, statedb, resultCh, exitCh, &wg)
		genblock(i, parent, statedb, locations[i], resultCh, exitCh)
		wg.Wait()
		blocks[i] = knot.block
		parent = knot.block
	}
	return blocks, receipts
}

func makeHeader(chain consensus.ChainReader, parent *types.Block, state *state.StateDB, engine consensus.Engine, location []byte) *types.Header {
	var time uint64
	if parent.Time() == 0 {
		time = 10
	} else {
		time = parent.Time() + 10 // block time is fixed at 10 seconds
	}

	baseFee := misc.CalcBaseFee(chain.Config(), parent.Header(), chain.GetHeaderByNumber, chain.GetUnclesInChain, chain.GetGasUsedInChain)

	header := &types.Header{
		Coinbase:    []common.Address{common.Address{}, common.Address{}, common.Address{}},
		Number:      []*big.Int{big.NewInt(int64(1)), big.NewInt(int64(1)), big.NewInt(int64(1))},
		ParentHash:  []common.Hash{common.Hash{}, common.Hash{}, common.Hash{}},
		Root:        []common.Hash{common.Hash{}, common.Hash{}, common.Hash{}},
		Difficulty:  []*big.Int{big.NewInt(1), big.NewInt(1), big.NewInt(1)},
		UncleHash:   types.EmptyUncleHash,
		TxHash:      types.EmptyRootHash,
		ReceiptHash: types.EmptyRootHash,
		Bloom:       []types.Bloom{types.Bloom{}, types.Bloom{}, types.Bloom{}},
		Time:        time,
		BaseFee:     []*big.Int{baseFee, baseFee, baseFee},
		GasLimit:    []uint64{0, 0, 0},
		GasUsed:     []uint64{0, 0, 0},
		Extra:       [][]byte{[]byte(nil), []byte(nil), []byte(nil)},
		Location:    location,
	}

	// Setting this to params.MinGasLimit for now since
	header.GasLimit = []uint64{params.MinGasLimit, params.MinGasLimit, params.MinGasLimit}

	parentHeader := parent.Header()

	header.ParentHash[types.QuaiNetworkContext] = parent.Hash()
	header.Coinbase[types.QuaiNetworkContext] = parent.Coinbase()
	header.Difficulty[types.QuaiNetworkContext] = engine.CalcDifficulty(chain, time, parentHeader, types.QuaiNetworkContext)

	header.Number[types.QuaiNetworkContext] = new(big.Int).Add(parent.Number(), common.Big1)
	if len(parent.Header().Location) > 0 && header.Location[0] == parent.Header().Location[0] {
		header.Number[types.QuaiNetworkContext+1] = new(big.Int).Add(parent.Header().Number[types.QuaiNetworkContext+1], common.Big1)
		header.ParentHash[types.QuaiNetworkContext+1] = parent.Hash()
	}

	return header
}

// makeHeaderChain creates a deterministic chain of headers rooted at parent.
func makeHeaderChain(parent *types.Header, n int, engine consensus.Engine, db ethdb.Database, seed int) []*types.Header {
	blocks := makeBlockChain(types.NewBlockWithHeader(parent), n, engine, db, seed)
	headers := make([]*types.Header, len(blocks))
	for i, block := range blocks {
		headers[i] = block.Header()
	}
	return headers
}

// makeBlockChain creates a deterministic chain of blocks rooted at parent.
func makeBlockChain(parent *types.Block, n int, engine consensus.Engine, db ethdb.Database, seed int) []*types.Block {
	blocks, _ := GenerateChain(params.TestChainConfig, parent, engine, db, n, func(i int, b *BlockGen) {
		b.SetCoinbase(common.Address{0: byte(seed), 19: byte(i)})
	})
	return blocks
}

// Generate blocks to form a network of chains
func GenerateNetworkBlocks(graph [3][3][]*types.BlockGenSpec) (map[string]*types.Block, error) {
	return nil, errors.New("Not implemented")
}

type fakeChainReader struct {
	config *params.ChainConfig
}

// Config returns the chain configuration.
func (cr *fakeChainReader) Config() *params.ChainConfig {
	return cr.config
}

func (cr *fakeChainReader) CurrentHeader() *types.Header                            { return nil }
func (cr *fakeChainReader) GetHeaderByNumber(number uint64) *types.Header           { return nil }
func (cr *fakeChainReader) GetHeaderByHash(hash common.Hash) *types.Header          { return nil }
func (cr *fakeChainReader) GetHeader(hash common.Hash, number uint64) *types.Header { return nil }
func (cr *fakeChainReader) GetBlock(hash common.Hash, number uint64) *types.Block   { return nil }
func (cr *fakeChainReader) GetExternalBlock(hash common.Hash, location []byte, context uint64) (*types.ExternalBlock, error) {
	return nil, nil
}
func (cr *fakeChainReader) QueueAndRetrieveExtBlocks(blocks []*types.ExternalBlock, header *types.Header) []*types.ExternalBlock {
	return nil
}

func (cr *fakeChainReader) GetUnclesInChain(block *types.Block, length int) []*types.Header {
	return nil
}
func (cr *fakeChainReader) GetGasUsedInChain(block *types.Block, length int) int64 { return 0 }

func (cr *fakeChainReader) GetExternalBlocks(header *types.Header) ([]*types.ExternalBlock, error) {
	return nil, nil
}

func (cr *fakeChainReader) GetLinkExternalBlocks(header *types.Header) ([]*types.ExternalBlock, error) {
	return nil, nil
}

// CheckContext checks to make sure the range of a context or order is valid
func (cr *fakeChainReader) CheckContext(context int) error {
	if context < 0 || context > len(params.FullerOntology) {
		return errors.New("the provided path is outside the allowable range")
	}
	return nil
}

// CheckLocationRange checks to make sure the range of r and z are valid
func (cr *fakeChainReader) CheckLocationRange(location []byte) error {
	if int(location[0]) < 1 || int(location[0]) > params.FullerOntology[0] {
		return errors.New("the provided location is outside the allowable region range")
	}
	if int(location[1]) < 1 || int(location[1]) > params.FullerOntology[1] {
		return errors.New("the provided location is outside the allowable zone range")
	}
	return nil
}
