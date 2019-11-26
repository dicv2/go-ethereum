// Copyright 2019 The go-ethereum Authors
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

package snapshot

import (
	"fmt"
	"math/big"
	"time"

	"github.com/VictoriaMetrics/fastcache"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/trie"
)

var (
	// emptyRoot is the known root hash of an empty trie.
	emptyRoot = common.HexToHash("56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421")

	// emptyCode is the known hash of the empty EVM bytecode.
	emptyCode = crypto.Keccak256Hash(nil)
)

// generateSnapshot regenerates a brand new snapshot based on an existing state
// database and head block asynchronously. The snapshot is returned immediately
// and generation is continued in the background until done.
func generateSnapshot(db ethdb.KeyValueStore, journal string, root common.Hash) (snapshot, error) {
	// Wipe any previously existing snapshot from the database
	if err := wipeSnapshot(db); err != nil {
		return nil, err
	}
	//
	panic("wtf")
}

// generateSnapshotSync regenerates a brand new snapshot based on an existing state database and head block.
func generateSnapshotSync(db ethdb.KeyValueStore, journal string, root common.Hash) (snapshot, error) {
	// Wipe any previously existing snapshot from the database
	if err := wipeSnapshot(db); err != nil {
		return nil, err
	}
	// Iterate the entire storage trie and re-generate the state snapshot
	var (
		accountCount int
		storageCount int
		storageNodes int
		accountSize  common.StorageSize
		storageSize  common.StorageSize
		logged       time.Time
	)
	batch := db.NewBatch()
	triedb := trie.NewDatabase(db)

	accTrie, err := trie.NewSecure(root, triedb)
	if err != nil {
		return nil, err
	}
	accIt := trie.NewIterator(accTrie.NodeIterator(nil))
	for accIt.Next() {
		var (
			curStorageCount int
			curStorageNodes int
			curAccountSize  common.StorageSize
			curStorageSize  common.StorageSize
			accountHash     = common.BytesToHash(accIt.Key)
		)
		var acc struct {
			Nonce    uint64
			Balance  *big.Int
			Root     common.Hash
			CodeHash []byte
		}
		if err := rlp.DecodeBytes(accIt.Value, &acc); err != nil {
			return nil, err
		}
		data := AccountRLP(acc.Nonce, acc.Balance, acc.Root, acc.CodeHash)
		curAccountSize += common.StorageSize(1 + common.HashLength + len(data))

		rawdb.WriteAccountSnapshot(batch, accountHash, data)
		if batch.ValueSize() > ethdb.IdealBatchSize {
			batch.Write()
			batch.Reset()
		}
		if acc.Root != emptyRoot {
			storeTrie, err := trie.NewSecure(acc.Root, triedb)
			if err != nil {
				return nil, err
			}
			storeIt := trie.NewIterator(storeTrie.NodeIterator(nil))
			for storeIt.Next() {
				curStorageSize += common.StorageSize(1 + 2*common.HashLength + len(storeIt.Value))
				curStorageCount++

				rawdb.WriteStorageSnapshot(batch, accountHash, common.BytesToHash(storeIt.Key), storeIt.Value)
				if batch.ValueSize() > ethdb.IdealBatchSize {
					batch.Write()
					batch.Reset()
				}
			}
			curStorageNodes = storeIt.Nodes
		}
		accountCount++
		storageCount += curStorageCount
		accountSize += curAccountSize
		storageSize += curStorageSize
		storageNodes += curStorageNodes

		if time.Since(logged) > 8*time.Second {
			fmt.Printf("%#x: %9s + %9s (%6d slots, %6d nodes), total %9s (%d accs, %d nodes) + %9s (%d slots, %d nodes)\n", accIt.Key, curAccountSize.TerminalString(), curStorageSize.TerminalString(), curStorageCount, curStorageNodes, accountSize.TerminalString(), accountCount, accIt.Nodes, storageSize.TerminalString(), storageCount, storageNodes)
			logged = time.Now()
		}
	}
	fmt.Printf("Totals: %9s (%d accs, %d nodes) + %9s (%d slots, %d nodes)\n", accountSize.TerminalString(), accountCount, accIt.Nodes, storageSize.TerminalString(), storageCount, storageNodes)

	// Update the snapshot block marker and write any remainder data
	rawdb.WriteSnapshotRoot(batch, root)
	batch.Write()
	batch.Reset()

	// Compact the snapshot section of the database to get rid of unused space
	log.Info("Compacting snapshot in chain database")
	if err := db.Compact([]byte{'s'}, []byte{'s' + 1}); err != nil {
		return nil, err
	}
	// New snapshot generated, construct a brand new base layer
	cache := fastcache.New(512 * 1024 * 1024)
	return &diskLayer{
		journal: journal,
		db:      db,
		cache:   cache,
		root:    root,
	}, nil
}
