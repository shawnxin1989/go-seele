/**
*  @file
*  @copyright defined in go-seele/LICENSE
 */

package discovery

import (
	"net"

	"github.com/seeleteam/go-seele/common"
)

func StartService(nodeDir string, myId common.Address, myAddr *net.UDPAddr, bootstrap []*Node) *Database {
	udp := newUDP(myId, myAddr)

	if bootstrap != nil {
		udp.trustNodes = bootstrap
	}
	udp.loadNodes(nodeDir)
	udp.StartServe(nodeDir)

	return udp.db
}
