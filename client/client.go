package main

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ogier/pflag"

	"github.com/malcolmseyd/natpunch-go/client/cmd"
	"github.com/malcolmseyd/natpunch-go/client/network"
	"github.com/malcolmseyd/natpunch-go/client/util"
)

const timeout = time.Second * 10
const persistentKeepalive = 25

func main() {
	pflag.Usage = printUsage

	continuous := pflag.BoolP("continuous", "c", false, "continuously resolve peers after they've already been resolved")
	delay := pflag.Float32P("delay", "d", 2.0, "time to wait between retries (in seconds)")

	pflag.Parse()
	args := pflag.Args()

	if len(args) < 2 {
		printUsage()
		os.Exit(1)
	}

	if os.Getuid() != 0 {
		fmt.Fprintln(os.Stderr, "Must be root!")
		os.Exit(1)
	}

	serverSplit := strings.Split(args[0], ":")
	serverHostname := serverSplit[0]
	if len(serverSplit) < 2 {
		fmt.Fprintln(os.Stderr, "Please include a port like this:", serverHostname+":PORT")
		os.Exit(1)
	}

	serverAddr := network.HostToAddr(serverHostname)

	serverPort, err := strconv.ParseUint(serverSplit[1], 10, 16)
	if err != nil {
		log.Fatalln("Error parsing server port:", err)
	}
	server := network.Server{
		Hostname: serverHostname,
		Addr:     serverAddr,
		Port:     uint16(serverPort),
	}
	ifaceName := args[1]

	run(ifaceName, server, *continuous, *delay)
}

func run(ifaceName string, server network.Server, continuous bool, delay float32) {
	// get the source ip that we'll send the packet from
	clientIP := network.GetClientIP(server.Addr.IP)

	cmd.RunCmd("wg-quick", "up", ifaceName)

	// get info about the Wireguard config
	clientPort := cmd.GetClientPort(ifaceName)
	clientPubkey := cmd.GetClientPubkey(ifaceName)

	client := network.Peer{
		IP:     clientIP,
		Port:   clientPort,
		Pubkey: clientPubkey,
	}

	peerKeysStr := cmd.GetPeers(ifaceName)
	var peers []network.Peer = util.MakePeerSlice(peerKeysStr)

	// we're using raw sockets to spoof the source port,
	// which is already being used by Wireguard
	rawConn := network.SetupRawConn(&server, &client)
	defer rawConn.Close()

	// payload consists of client key + peer key
	payload := make([]byte, 64)
	copy(payload[0:32], clientPubkey[:])

	totalPeers := len(peers)
	resolvedPeers := 0

	fmt.Println("Resolving", totalPeers, "peers")

	// we keep requesting if the server doesn't have one of our peers.
	// this keeps running until all connections are established.
	tryAgain := true
	for tryAgain {
		tryAgain = false
		for i, peer := range peers {
			if peer.Resolved && !continuous {
				continue
			}
			fmt.Printf("(%d/%d) %s: ", resolvedPeers, totalPeers, base64.RawStdEncoding.EncodeToString(peer.Pubkey[:])[:16])
			copy(payload[32:64], peer.Pubkey[:])

			err := network.SendPacket(payload, rawConn, &server, &client)
			if err != nil {
				log.Println("\nError sending packet:", err)
				continue
			}

			response, n, err := network.RecvPacket(rawConn, timeout, &server, &client)
			if err != nil {
				if err, ok := err.(net.Error); ok && err.Timeout() {
					fmt.Println("\nConnection to", server.Hostname, "timed out.")
					tryAgain = true
					continue
				}
				fmt.Println("\nError receiving packet:", err)
				continue
			}

			if n == network.EmptyUDPSize {
				fmt.Println("not found")
				tryAgain = true
				continue
			} else if n < network.EmptyUDPSize {
				log.Println("\nError: response is not a valid udp packet")
				continue
			} else if n != network.EmptyUDPSize+4+2 {
				// expected packet size, 4 bytes for ip, 2 for port
				log.Println("\nError: invalid response of length", n)
				// For debugging
				fmt.Println(hex.Dump(response[:n]))
				tryAgain = true
				continue
			}

			peer.IP, peer.Port = network.ParseResponse(response)
			if peer.IP == nil {
				log.Println("Error parsing packet: not a valid UDP packet")
			}
			if !peer.Resolved {
				peer.Resolved = true
				resolvedPeers++
			}

			fmt.Println(peer.IP.String() + ":" + strconv.FormatUint(uint64(peer.Port), 10))
			cmd.SetPeer(&peer, persistentKeepalive, ifaceName)

			peers[i] = peer

			if continuous {
				// always try again if continuous
				tryAgain = true
			}
		}
		if tryAgain {
			time.Sleep(time.Second * time.Duration(delay))
		}
	}
	fmt.Print("Resolved ", resolvedPeers, " peer")
	if totalPeers != 1 {
		fmt.Print("s")
	}
	fmt.Print("\n")
}

func printUsage() {
	fmt.Fprintf(os.Stderr,
		"Usage: %s [OPTION]... SERVER_HOSTNAME:PORT WIREGUARD_INTERFACE\n"+
			"Flags:\n", os.Args[0],
	)
	pflag.PrintDefaults()
	fmt.Fprintf(os.Stderr,
		"Example:\n"+
			"    %s demo.wireguard.com:12345 wg0\n",
		os.Args[0],
	)
}
