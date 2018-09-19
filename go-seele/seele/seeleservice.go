/**
*  @file
*  @copyright defined in go-seele/LICENSE
 */

package seele

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/seeleteam/go-seele/common"
	"github.com/seeleteam/go-seele/core"
	"github.com/seeleteam/go-seele/core/store"
	"github.com/seeleteam/go-seele/core/state"
	"github.com/seeleteam/go-seele/database"
	"github.com/seeleteam/go-seele/database/leveldb"
	"github.com/seeleteam/go-seele/event"
	"github.com/seeleteam/go-seele/log"
	"github.com/seeleteam/go-seele/miner"
	"github.com/seeleteam/go-seele/node"
	"github.com/seeleteam/go-seele/p2p"
	rpc "github.com/seeleteam/go-seele/rpc2"
	"github.com/seeleteam/go-seele/seele/download"
)

const chainHeaderChangeBuffSize = 100

// SeeleService implements full node service.
type SeeleService struct {
	networkID     uint64
	p2pServer     *p2p.Server
	seeleProtocol *SeeleProtocol
	log           *log.SeeleLog

	txPools         [numOfChains]*core.TransactionPool
	debtPools       [numOfChains]*core.DebtPool
	chains          [numOfChains]*core.Blockchain
	chainDBs        [numOfChains]database.Database // database used to store blocks.
	accountStateDB database.Database // database used to store account state info.
	accountStateDBRootHash common.Hash
	miner          *miner.Miner

	lastHeaders              [numOfChains]common.Hash
	chainHeaderChangeChannels [numOfChains]chan common.Hash
}

// ServiceContext is a collection of service configuration inherited from node
type ServiceContext struct {
	DataDir string
}

func (s *SeeleService) TxPool() []*core.TransactionPool { return s.txPools }
func (s *SeeleService) DebtPool() []*core.DebtPool      { return s.debtPools }
func (s *SeeleService) BlockChain() []*core.Blockchain  { return s.chains }
func (s *SeeleService) NetVersion() uint64            { return s.networkID }
func (s *SeeleService) Miner() *miner.Miner           { return s.miner }
func (s *SeeleService) Downloader() *downloader.Downloader {
	return s.seeleProtocol.Downloader()
}
func (s *SeeleService) AccountStateDB() database.Database { return s.accountStateDB }
// GetCurrentState returns the current state of the accounts
func (s *SeeleService) GetCurrentState() (*state.Statedb, error) {
	return state.NewStatedb(s.accountStateDBRootHash, s.accountStateDB)
}


// NewSeeleService create SeeleService
func NewSeeleService(ctx context.Context, conf *node.Config, log *log.SeeleLog) (s *SeeleService, err error) {
	s = &SeeleService{
		log:       log,
		networkID: conf.P2PConfig.NetworkID,
	}

	serviceContext := ctx.Value("ServiceContext").(ServiceContext)

	// Initialize blockchain DB.
	for i := 0; i < numOfChains; i++ {
		chainNumString := strconv.Itoa(i)
		chainDBPath := filepath.Join(serviceContext.DataDir, BlockChainDir, chainNumString)
		log.Info("NewSeeleService BlockChain datadir is %s", chainDBPath)	
		s.chainDBs[i],err = leveldb.NewLevelDB(chainDBPath)
		if err != nil {
			log.Error("NewSeeleService Create BlockChain err. %s", err)
			return nil, err
		}
		leveldb.StartMetrics(s.chainDBs[i], "chaindb"+chainNumString, log)
	}
	
	// Initialize account state info DB.
	accountStateDBPath := filepath.Join(serviceContext.DataDir, AccountStateDir)
	log.Info("NewSeeleService account state datadir is %s", accountStateDBPath)
	s.accountStateDB, err = leveldb.NewLevelDB(accountStateDBPath)
	if err != nil {
		for i := 0; i < numOfChains; i++ {
			s.chainDBs[i].Close()
		}
		log.Error("NewSeeleService Create BlockChain err: failed to create account state DB, %s", err)
		return nil, err
	}

	// initialize accountStateDB with genesis info
	statedb, err := core.getStateDB(genesis.info)
	if err != nil {
		return err
	}

	s.accountStateDBRootHash, err := statedb.Hash()
	if err != nil {
		return err
	}

	batch := s.accountStateDB.NewBatch()
	statedb.Commit(batch)
	if err = batch.Commit(); err != nil {
		return err
	}

	// initialize and validate genesis
	for i := 0; i < numOfChains; i++ {
		bcStore := store.NewCachedStore(store.NewBlockchainDatabase(s.chainDBs[i]))
		genesis := core.GetGenesis(conf.SeeleConfig.GenesisConfig)
		err = genesis.InitializeAndValidate(bcStore)
		if err != nil {
			for i := 0; i < numOfChains; i++ {
				s.chainDBs[i].Close()
			}
			s.accountStateDB.Close()
			log.Error("NewSeeleService genesis.Initialize err. %s", err)
			return nil, err
		}
	
		chainNumString = strconv.Itoa(i)
		recoveryPointFile := filepath.Join(serviceContext.DataDir, BlockChainRecoveryPointFile, chainNumString)
		s.chains[i], err = core.NewBlockchain(bcStore, recoveryPointFile, uint64(i))
		if err != nil {
			for i := 0; i < numOfChains; i++ {
				s.chainDBs[i].Close()
			}
			s.accountStateDB.Close()
			log.Error("failed to init chain in NewSeeleService. %s", err)
			return nil, err
		}
	}

	err = s.initPool(conf)
	if err != nil {
		for i := 0; i < numOfChains; i++ {
			s.chainDBs[i].Close()
		}
		s.accountStateDB.Close()
		log.Error("failed to create transaction pool in NewSeeleService, %s", err)
		return nil, err
	}
	

	s.seeleProtocol, err = NewSeeleProtocol(s, log)
	if err != nil {
		for i := 0; i < numOfChains; i++ {
			s.chainDBs[i].Close()
		}
		s.accountStateDB.Close()
		log.Error("failed to create seeleProtocol in NewSeeleService, %s", err)
		return nil, err
	}

	s.miner = miner.NewMiner(conf.SeeleConfig.Coinbase, s)

	return s, nil
}

func (s *SeeleService) initPool(conf *node.Config) error {
	var err error
	for i := 0; i < numOfChains; i++ {
		s.lastHeaders[i], err = s.chains[i].GetStore().GetHeadBlockHash()
		if err != nil {
			return fmt.Errorf("failed to get chain header, %s", err)
		}

		s.chainHeaderChangeChannels[i] = make(chan common.Hash, chainHeaderChangeBuffSize)
		s.debtPools[i] = core.NewDebtPool(s.chains[i])
		s.txPools[i] = core.NewTransactionPool(conf.SeeleConfig.TxConf, s.chains[i])

		event.ChainHeaderChangedEventMananger.AddAsyncListener(s.chainHeaderChanged)
		go s.MonitorChainHeaderChange(uint64(i))

		return nil
	}
}

// chainHeaderChanged handle chain header changed event.
// add forked transaction back
// deleted invalid transaction
func (s *SeeleService) chainHeaderChanged(e event.Event) {
	newHeader := e.(*event.chainHeaderChangedMsg).HeaderHash 
	if newHeader.IsEmpty() {
		return
	}
	chainNum := e.(*event.chainHeaderChangedMsg).chainNum
	s.chainHeaderChangeChannels[chainNum] <- newHeader
}

// MonitorChainHeaderChange monitor and handle chain header event
func (s *SeeleService) MonitorChainHeaderChange(chainNum uint64) {
	for {
		select {
		case newHeader := <-s.chainHeaderChangeChannels[chainNum]:
			if s.lastHeaders[chainNum].IsEmpty() {
				s.lastHeaders[chainNum] = newHeader
				return
			}

			s.txPools[chainNum].HandleChainHeaderChanged(newHeader, s.lastHeaders[chainNum])
			//s.debtPool.HandleChainHeaderChanged(newHeader, s.lastHeader)

			s.lastHeaders[chainNum] = newHeader
		}
	}
}

// Protocols implements node.Service, returning all the currently configured
// network protocols to start.
func (s *SeeleService) Protocols() (protos []p2p.Protocol) {
	protos = append(protos, s.seeleProtocol.Protocol)
	return
}

// Start implements node.Service, starting goroutines needed by SeeleService.
func (s *SeeleService) Start(srvr *p2p.Server) error {
	s.p2pServer = srvr

	s.seeleProtocol.Start()
	return nil
}

// Stop implements node.Service, terminating all internal goroutines.
func (s *SeeleService) Stop() error {
	s.seeleProtocol.Stop()

	//TODO
	// s.txPool.Stop() s.chain.Stop()
	// retries? leave it to future
	s.chainDB.Close()
	s.accountStateDB.Close()
	return nil
}

// APIs implements node.Service, returning the collection of RPC services the seele package offers.
func (s *SeeleService) APIs() (apis []rpc.API) {
	return append(apis, []rpc.API{
		{
			Namespace: "seele",
			Version:   "1.0",
			Service:   NewPublicSeeleAPI(s),
			Public:    true,
		},
		{
			Namespace: "txpool",
			Version:   "1.0",
			Service:   NewTransactionPoolAPI(s),
			Public:    true,
		},
		{
			Namespace: "download",
			Version:   "1.0",
			Service:   downloader.NewPrivatedownloaderAPI(s.seeleProtocol.downloader),
			Public:    false,
		},
		{
			Namespace: "network",
			Version:   "1.0",
			Service:   NewPrivateNetworkAPI(s),
			Public:    false,
		},
		{
			Namespace: "debug",
			Version:   "1.0",
			Service:   NewPrivateDebugAPI(s),
			Public:    false,
		},
		{
			Namespace: "miner",
			Version:   "1.0",
			Service:   NewPrivateMinerAPI(s),
			Public:    false,
		},
	}...)
}
