/**
*  @file
*  @copyright defined in go-seele/LICENSE
 */

package seele

import (
	"math/big"
	"sync"

	"github.com/seeleteam/go-seele/common"
)

type peerSet struct {
	peerMap    map[common.Address]*peer
	shardPeers [1 + common.ShardCount]map[common.Address]*peer
	lock       sync.RWMutex
}

type bestPeerForEachChain struct {
	bestPeer	*peer
	bestTd		*big.int		// the Td of the peer in this chain	
	chainNum    uint64	        // the peer is the best in this chain
}

func newPeerSet() *peerSet {
	ps := &peerSet{
		peerMap: make(map[common.Address]*peer),
		lock:    sync.RWMutex{},
	}

	for i := 0; i < 1+common.ShardCount; i++ {
		ps.shardPeers[i] = make(map[common.Address]*peer)
	}

	return ps
}

func (p *peerSet) bestPeer(shard uint) []*bestPeerForEachChain {
	
	bestPeers := make([]*bestPeerForEachChain, numOfChains)
	p.ForEach(shard, func(p *peer) bool {
 		for i := 0; i < numOfChains; i++ {
			td := p.HeadByChain(i)   
			if bestPeers[i] == nil || td.Cmp(bestPeers[i].bestTd) > 0 {
				bestPeers[i].bestPeer, bestPeers[i].bestTd = p, td
 				bestPeers[i].chainNum = i
 			}
		}

		return true
	})

	return bestPeer
}

func (p *peerSet) Find(address common.Address) *peer {
	p.lock.RLock()
	defer p.lock.RUnlock()

	return p.peerMap[address]
}

func (p *peerSet) Remove(address common.Address) {
	p.lock.Lock()
	defer p.lock.Unlock()

	result := p.peerMap[address]
	if result != nil {
		delete(p.peerMap, address)
		delete(p.shardPeers[result.Node.Shard], address)
	}
}

func (p *peerSet) Add(pe *peer) {
	p.lock.Lock()
	defer p.lock.Unlock()

	address := pe.Node.ID
	result := p.peerMap[address]
	if result != nil {
		delete(p.peerMap, address)
		delete(p.shardPeers[result.Node.Shard], address)
	}

	p.peerMap[address] = pe
	p.shardPeers[pe.Node.Shard][address] = pe
}

func (p *peerSet) ForEach(shard uint, handle func(*peer) bool) {
	p.lock.RLock()
	defer p.lock.RUnlock()

	for _, v := range p.shardPeers[shard] {
		if !handle(v) {
			break
		}
	}
}

func (p *peerSet) ForEachAll(handle func(*peer) bool) {
	p.lock.RLock()
	defer p.lock.RUnlock()

	for _, v := range p.peerMap {
		if !handle(v) {
			break
		}
	}
}

func (p *peerSet) getPeerByShard(shard uint) []*peer {
	p.lock.RLock()
	defer p.lock.RUnlock()

	value := make([]*peer, len(p.shardPeers[shard]))
	index := 0
	for _, v := range p.shardPeers[shard] {
		value[index] = v
		index++
	}

	return value
}

func (p *peerSet) getPeerCountByShard(shard uint) int {
	p.lock.RLock()
	defer p.lock.RUnlock()

	return len(p.shardPeers[shard])
}
