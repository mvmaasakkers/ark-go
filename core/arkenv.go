package core

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dghubble/sling"
	"github.com/kristjank/ark-go/arkcoin"
)

//ArkNetworkType for network switching
type ArkNetworkType int

const (
	//MAINNET connection
	MAINNET = iota
	//DEVNET connection
	DEVNET
)

//TO HELP DIVIDE
//TODO rename
const (
	SATOSHI = 100000000
)

//EnvironmentParams - Global ARK EnvironmentParams read from acitve peers
var EnvironmentParams = new(ArkEnvParams)

//ArkEnvParams structure to hold parameters from autoconfigure
//structure is filled from peers at arkclient init call
type ArkEnvParams struct {
	Success bool    `json:"success"`
	Network Network `json:"network"`
	Fees    Fees    `json:"fees"`
}

//Fees constant parameters for active configuration
type Fees struct {
	Send            int64 `json:"send"`
	Vote            int64 `json:"vote"`
	SecondSignature int64 `json:"secondsignature"`
	Delegate        int64 `json:"delegate"`
	MultiSignature  int64 `json:"multisignature"`
}

//Network parameters
type Network struct {
	Nethash        string         `json:"nethash"`
	Token          string         `json:"token"`
	Symbol         string         `json:"symbol"`
	Explorer       string         `json:"explorer"`
	AddressVersion byte           `json:"version"` //this is address generator version!!!
	Type           ArkNetworkType //holding ark networktype
	ActivePeer     Peer
	PeerList       []Peer
}

//LoadActiveConfiguration reads arknetwork parameters from the Network
//and fills the EnvironmentParams structure
//selected and connected peer address is returned
func LoadActiveConfiguration(arknetwork ArkNetworkType) string {
	s1 := rand.NewSource(time.Now().UnixNano())
	r1 := rand.New(s1)

	selectedPeer := ""
	EnvironmentParams.Network.Type = arknetwork
	//looping peers comunication until we get autoconfigure response
	i := 0
	for selectedPeer == "" && i < 10 {

		i++
		switch arknetwork {
		case MAINNET:
			log.Println("Active network is MAINNET")
			selectedPeer = seedList[r1.Intn(len(seedList))]
			log.Println("Random peer selected: ", selectedPeer)

		case DEVNET:
			log.Println("Active network is DEVNET")
			selectedPeer = testSeedList[r1.Intn(len(testSeedList))]
			log.Println("Random peer selected: ", selectedPeer)
		}
		log.Println("Connecting to random peer", selectedPeer, r1.Intn(len(seedList)))
		//reading basic network params
		res, err := http.Get("http://" + selectedPeer + "/api/loader/autoconfigure")
		if err != nil {
			log.Println("Error receiving autoloader params rest from: ", selectedPeer, " Error: ", err.Error())
			selectedPeer = ""
		}
		json.NewDecoder(res.Body).Decode(&EnvironmentParams)
	}

	if selectedPeer == "" {
		log.Fatal("Unable to connect to blockchain, exiting")
	}

	//reading fees
	res, err := http.Get("http://" + selectedPeer + "/api/blocks/getfees")
	if err != nil {
		log.Fatal("Error receiving fees params rest from: ", selectedPeer)
	}
	json.NewDecoder(res.Body).Decode(&EnvironmentParams)

	//getting connected peer params from peer
	peerParams := strings.Split(selectedPeer, ":")
	peerRes := new(PeerResponse)
	res, err = http.Get("http://" + selectedPeer + "/api/peers/get/?ip=" + peerParams[0] + "&port=" + peerParams[1])
	if err != nil {
		log.Fatal("Error receiving peer status from: ", selectedPeer, err.Error(), res.StatusCode)
	}
	json.NewDecoder(res.Body).Decode(peerRes)
	//saving peer parameters to globals
	EnvironmentParams.Network.ActivePeer = peerRes.SinglePeer

	return "http://" + optimizePeerList(selectedPeer)
}

func optimizePeerList(selectedPeer string) string {
	tmpClient := &ArkClient{
		sling: sling.New().Client(nil).Base("http://"+selectedPeer).
			Add("nethash", EnvironmentParams.Network.Nethash).
			Add("version", EnvironmentParams.Network.ActivePeer.Version).
			Add("port", strconv.Itoa(EnvironmentParams.Network.ActivePeer.Port)).
			Add("Content-Type", "application/json"),
	}

	peerResp, err, _ := tmpClient.GetAllPeers()
	if err.ErrorObj != nil {
		log.Println("Error getting peer list")
		return selectedPeer
	}

	EnvironmentParams.Network.PeerList = peerResp.Peers
	log.Println("Start to optimize peer list, currently ", len(EnvironmentParams.Network.PeerList), " peers.")

	//setting the version condition
	versionString := "1.0.1"
	if EnvironmentParams.Network.Type == DEVNET {
		versionString = "1.1.0"
	}

	//Clean the peer list (filters not working as they shoud) - so checking again here
	maxHeight := EnvironmentParams.Network.ActivePeer.Height
	for i := len(EnvironmentParams.Network.PeerList) - 1; i >= 0; i-- {
		peer := EnvironmentParams.Network.PeerList[i]

		// Condition to decide if current element has to be deleted:
		if peer.Status != "OK" || peer.Port != EnvironmentParams.Network.ActivePeer.Port || peer.Version != versionString {
			EnvironmentParams.Network.PeerList = append(EnvironmentParams.Network.PeerList[:i], EnvironmentParams.Network.PeerList[i+1:]...)
			//log.Println("Removing peer", peer.IP, peer.Status, peer.Height)
			continue
		}
		//if all is ok and height is higher - we preffer peers with higher hight
		if peer.Height > maxHeight {
			log.Println("Setting new active peer, found OK peer with bigger block height", peer.Height, maxHeight)
			EnvironmentParams.Network.ActivePeer = peer
			selectedPeer = fmt.Sprintf("%s:%d", peer.IP, peer.Port)
			maxHeight = peer.Height
		}
	}

	//removing peers with difference more then 10 blocks, that is 10x8s behing mainheight
	for i := len(EnvironmentParams.Network.PeerList) - 1; i >= 0; i-- {
		peer := EnvironmentParams.Network.PeerList[i]

		if maxHeight-peer.Height > 10 {
			EnvironmentParams.Network.PeerList = append(EnvironmentParams.Network.PeerList[:i], EnvironmentParams.Network.PeerList[i+1:]...)
			//log.Println("Removing peer, based on maxheight difference condition", peer.IP, peer.Status, peer.Height)
			continue
		}
	}
	log.Println("End of peer optimization, remaining ", len(EnvironmentParams.Network.PeerList), " peers.")
	return selectedPeer
}

func switchNetwork(arkNetwork ArkNetworkType) {
	BaseURL = LoadActiveConfiguration(arkNetwork)
	var wifHeader = []byte{170}

	if arkNetwork == DEVNET {
		wifHeader = []byte{239}
	}

	coinParams := arkcoin.Params{
		AddressHeader:          EnvironmentParams.Network.AddressVersion,
		DumpedPrivateKeyHeader: wifHeader,
	}
	arkcoin.SetActiveCoinConfiguration(&coinParams)
}

//SetActiveConfiguration sets a new client connection, switches network and reads network settings from peer
//usage - must reassing new pointer value: arkapi = arkapi.SetActiveConfiguration(MAINNET)
func (s *ArkClient) SetActiveConfiguration(arkNetwork ArkNetworkType) *ArkClient {
	switchNetwork(arkNetwork)
	return NewArkClient(nil)
}

var seedList = [...]string{
	"5.39.9.240:4001",
	"5.39.9.241:4001",
	"5.39.9.242:4001",
	"5.39.9.243:4001",
	"5.39.9.244:4001",
	"5.39.9.250:4001",
	"5.39.9.251:4001",
	"5.39.9.252:4001",
	"5.39.9.253:4001",
	"5.39.9.254:4001",
	"5.39.9.255:4001",
	"5.39.53.48:4001",
	"5.39.53.49:4001",
	"5.39.53.50:4001",
	"5.39.53.51:4001",
	"5.39.53.52:4001",
	"5.39.53.53:4001",
	"5.39.53.54:4001",
	"5.39.53.55:4001",
	"37.59.129.160:4001",
	"37.59.129.161:4001",
	"37.59.129.162:4001",
	"37.59.129.163:4001",
	"37.59.129.164:4001",
	"37.59.129.165:4001",
	"37.59.129.166:4001",
	"37.59.129.167:4001",
	"37.59.129.168:4001",
	"37.59.129.169:4001",
	"37.59.129.170:4001",
	"37.59.129.171:4001",
	"37.59.129.172:4001",
	"37.59.129.173:4001",
	"37.59.129.174:4001",
	"37.59.129.175:4001",
	"193.70.72.80:4001",
	"193.70.72.81:4001",
	"193.70.72.82:4001",
	"193.70.72.83:4001",
	"193.70.72.84:4001",
	"193.70.72.85:4001",
	"193.70.72.86:4001",
	"193.70.72.87:4001",
	"193.70.72.88:4001",
	"193.70.72.89:4001",
	"193.70.72.90:4001"}

var testSeedList = [...]string{
	"167.114.43.48:4002",
	"167.114.29.49:4002",
	"167.114.43.43:4002",
	"167.114.29.54:4002",
	"167.114.29.45:4002",
	"167.114.29.40:4002",
	"167.114.29.56:4002",
	"167.114.43.35:4002",
	"167.114.29.51:4002",
	"167.114.29.59:4002",
	"167.114.43.42:4002",
	"167.114.29.34:4002",
	"167.114.29.62:4002",
	"167.114.43.49:4002",
	"167.114.29.44:4002",
	"167.114.43.37:4002",
	"167.114.29.63:4002",
	"167.114.29.42:4002",
	"167.114.29.48:4002",
	"167.114.29.61:4002",
	"167.114.43.36:4002",
	"167.114.29.57:4002",
	"167.114.43.33:4002",
	"167.114.29.52:4002",
	"167.114.29.50:4002",
	"167.114.43.47:4002",
	"167.114.29.47:4002",
	"167.114.29.36:4002",
	"167.114.29.35:4002",
	"167.114.43.39:4002",
	"167.114.43.45:4002",
	"167.114.29.46:4002",
	"167.114.29.41:4002",
	"167.114.43.34:4002",
	"167.114.29.43:4002",
	"167.114.43.41:4002",
	"167.114.29.60:4002",
	"167.114.43.32:4002",
	"167.114.29.55:4002",
	"167.114.29.53:4002",
	"167.114.29.38:4002",
	"167.114.43.40:4002",
	"167.114.29.32:4002",
	"167.114.43.46:4002",
	"167.114.43.38:4002",
	"167.114.29.33:4002",
	"167.114.43.44:4002",
	"167.114.43.50:4002",
	"167.114.29.37:4002",
	"167.114.29.58:4002",
	"167.114.29.39:4002"}
