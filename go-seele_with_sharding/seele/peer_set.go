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
	lock       sync.RWMutex
}

type bestPeerForEachShard struct {
	bestPeer	*peer
	bestTd		*big.int		// the Td of the peer in this shard	
	shard		uint	        // the peer is the best in this shard
}

func newPeerSet() *peerSet {
	ps := &peerSet{
		peerMap: make(map[common.Address]*peer),
		lock:    sync.RWMutex{},
	}

	return ps
}

// TODO: get bestPeer for each shard, add numOfShard info, modify p.Head()
func (p *peerSet) bestPeer() []*bestPeerForEachShard {
	
	bestPeers := make([]*bestPeerForEachShard, numOfShard)

	// traverse all the peers to get the bestPeer for each shard
	p.ForEach(func(p *peer) bool {
		for i := 0; i < numOfShard; i++ {
			td := p.HeadByShard(i)   
			if bestPeers[i] == nil || td.Cmp(bestPeers[i].bestTd) > 0 {
				bestPeers[i].bestPeer, bestPeers[i].bestTd = p, td
				bestPeers[i].shard = i
			}
		}

		return true
	})

	return bestPeers
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

func (p *peerSet) ForEach(handle func(*peer) bool) {
	p.lock.RLock()
	defer p.lock.RUnlock()

	for _, v := range p.peerMap {
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
