package main

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/flynn/noise"
	"github.com/malcolmseyd/natpunch-go/server/auth"
	"golang.org/x/crypto/curve25519"
)

const (
	// PacketRegisterEndpointRequest identifies handhshake initiation packets
	PacketRegisterEndpointRequest byte = 1
	// PacketHandshakeResp identifies handhshake response packets
	PacketHandshakeResp byte = 2
	// PacketData identifies regular data packets.
	PacketData byte = 3
)

var (
	// ErrPacketType is returned when an unexepcted packet type is enountered
	ErrPacketType = errors.New("server: incorrect packet type")
	// ErrPeerNotFound is returned when the requested peer is not found
	ErrPeerNotFound = errors.New("server: peer not found")
	// ErrPubkey is returned when the public key recieved does not match the one we expect
	ErrPubkey = errors.New("server: public key did not match expected one")
	// ErrOldTimestamp is returned when a handleRegisterEndpointRequest timestamp isn't newer than the previous one
	ErrOldTimestamp = errors.New("server: handleRegisterEndpointRequest timestamp isn't new")
	// ErrNoTimestampPassword is returned when the handleRegisterEndpointRequest packet doesn't contain a timestamp and password (empty byte array)
	ErrNoTimestampPassword = errors.New("server: handleRegisterEndpointRequest had no timestamp and password")
	// ErrIncorrectHandshakeRequestLength is returned when the handleRegisterEndpointRequest packet doesn't contain enough bytes
	ErrIncorrectHandshakeRequestLength = errors.New("server: handleRegisterEndpointRequest got an incorrect number of bytes from the encrypted channel")
	// ErrIncorrectPassword is returned when the password provided by the client mismatches the correct one.
	ErrIncorrectPassword = errors.New("server: password is incorrect")
	// ErrNonce is returned when the nonce on a packet isn't valid
	ErrNonce = errors.New("client/network: invalid nonce")

	timeout = 5 * time.Second

	noiseConfig = noise.Config{
		CipherSuite: noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashBLAKE2s),
		Random:      rand.Reader,
		Pattern:     noise.HandshakeIK,
		Initiator:   false,
		Prologue:    []byte("natpunch-go is the best :)"),
	}
)

// Key stores a Wireguard key
type Key [32]byte

func (k *Key) clamp() {
	k[0] &= 248
	k[31] = (k[31] & 127) | 64
}

// Password stores a password
type Password [32]byte

// we use pointers on these maps so that two maps can link to one object

// PeerMap stores the peers by key
type PeerMap map[Key]*Peer

// IndexMap stores the Peers by index
type IndexMap map[uint32]*Peer

// Peer represents a Wireguard peer.
type Peer struct {
	ip     net.IP
	port   uint16
	pubkey Key

	index      uint32
	send, recv *auth.CipherState
	// UnixNano cast to uint64
	lastHandshake uint64
}

type state struct {
	conn     *net.UDPConn
	keyMap   PeerMap
	indexMap IndexMap
	privKey  Key
	password Password
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "Usage:", os.Args[0], "<PORT> <PASSWORD> [PRIVATE_KEY]")
		os.Exit(1)
	}

	s := state{}
	var err error

	port := os.Args[1]

	// Process password argument
	pass, err := base64.StdEncoding.DecodeString(os.Args[2])
	if err != nil || len(pass) != 32 {
		fmt.Fprintln(os.Stderr, "Error parsing password")
	}
	copy(s.password[:], pass)

	// Process private key argument
	if len(os.Args) > 3 {
		priv, err := base64.StdEncoding.DecodeString(os.Args[3])
		if err != nil || len(priv) != 32 {
			fmt.Fprintln(os.Stderr, "Error parsing private key")
		}
		copy(s.privKey[:], priv)
	} else {
		rand.Read(s.privKey[:])
		s.privKey.clamp()
	}

	pubkey, _ := curve25519.X25519(s.privKey[:], curve25519.Basepoint)
	fmt.Println("Starting nat-punching server on port", port)
	fmt.Println("Public key:", base64.StdEncoding.EncodeToString(pubkey))

	s.keyMap = make(PeerMap)
	s.indexMap = make(IndexMap)

	// the client can only handle IPv4 addresses right now.
	listenAddr, err := net.ResolveUDPAddr("udp4", ":"+port)
	if err != nil {
		log.Panicln("Error getting UDP address", err)
	}

	s.conn, err = net.ListenUDP("udp4", listenAddr)
	if err != nil {
		log.Panicln("Error getting UDP listen connection")
	}

	for {
		err := s.handleConnection()
		if err != nil {
			fmt.Println("Error handling the connection", err)
		}
	}
}

func (s *state) handleConnection() error {
	packet := make([]byte, 4096)

	n, clientAddr, err := s.conn.ReadFromUDP(packet)
	if err != nil {
		return err
	}
	packet = packet[:n]

	packetType := packet[0]
	packet = packet[1:]

	if packetType == PacketRegisterEndpointRequest {
		return s.handleRegisterEndpointRequest(packet, clientAddr, timeout)
	} else {
		fmt.Println("Unknown packet type:", packetType)
		fmt.Println(hex.Dump(packet))
	}

	return nil
}

//func (s *state) handleRequestEndpoint(packet []byte, clientAddr *net.UDPAddr, timeout time.Duration) (err error) {
//	index := binary.BigEndian.Uint32(packet[:4])
//	packet = packet[4:]
//
//	client, ok := s.indexMap[index]
//	if !ok {
//		return
//	}
//
//	nonce := binary.BigEndian.Uint64(packet[:8])
//	packet = packet[8:]
//	// println("recving nonce", nonce)
//
//	client.recv.SetNonce(nonce)
//	plaintext, err := client.recv.Decrypt(nil, nil, packet)
//	if err != nil {
//		return
//	}
//	if !client.recv.CheckNonce(nonce) {
//		// no need to throw an error, just return
//		return
//	}
//
//	clientPubKey := plaintext[:32]
//	plaintext = plaintext[32:]
//
//	if !bytes.Equal(clientPubKey, client.pubkey[:]) {
//		err = ErrPubkey
//		return
//	}
//
//	var targetPubKey Key
//	copy(targetPubKey[:], plaintext[:32])
//	// for later use
//	plaintext = plaintext[:6]
//
//	client.ip = clientAddr.IP
//	client.port = uint16(clientAddr.Port)
//
//	targetPeer, peerExists := s.keyMap[targetPubKey]
//	if peerExists {
//		// client must be ipv4 so this will never return nil
//		copy(plaintext[:4], targetPeer.ip.To4())
//		binary.BigEndian.PutUint16(plaintext[4:6], targetPeer.port)
//	} else {
//		// return nothing if peer not found
//		plaintext = plaintext[:0]
//	}
//
//	nonceBytes := make([]byte, 8)
//	binary.BigEndian.PutUint64(nonceBytes, client.send.Nonce())
//
//	header := append([]byte{PacketData}, nonceBytes...)
//	// println("sent nonce:", client.send.Nonce())
//	// println("sending", len(plaintext), "bytes")
//	response := client.send.Encrypt(header, nil, plaintext)
//
//	_, err = s.conn.WriteToUDP(response, clientAddr)
//	if err != nil {
//		return
//	}
//
//	fmt.Print(
//		base64.StdEncoding.EncodeToString(client.pubkey[:])[:16],
//		" ==> ",
//		base64.StdEncoding.EncodeToString(targetPubKey[:])[:16],
//		": ",
//	)
//
//	if peerExists {
//		fmt.Println("CONNECTED")
//	} else {
//		fmt.Println("NOT FOUND")
//	}
//
//	return
//}

func getTimestampAndPassword(handshake *noise.HandshakeState, packet []byte) (timestamp uint64, password []byte, err error) {
	timestampPasswordBytes, _, _, err := handshake.ReadMessage(nil, packet)
	if err != nil {
		return 0, nil, err
	}
	if len(timestampPasswordBytes) == 0 {
		return 0, nil, ErrNoTimestampPassword
	}

	if len(timestampPasswordBytes) != 4+32 {
		return 0, nil, ErrIncorrectHandshakeRequestLength
	}

	timestampBytes := timestampPasswordBytes[:4]
	timestampPasswordBytes = timestampPasswordBytes[4:]
	passwordBytes := timestampPasswordBytes[:32]

	timestamp = binary.BigEndian.Uint64(timestampBytes)
	return timestamp, passwordBytes, nil
}

func (s *state) handleRegisterEndpointRequest(packet []byte, clientAddr *net.UDPAddr, timeout time.Duration) (err error) {
	config := noiseConfig
	config.StaticKeypair = noise.DHKey{
		Private: s.privKey[:],
	}
	config.StaticKeypair.Public, err = curve25519.X25519(config.StaticKeypair.Private, curve25519.Basepoint)
	if err != nil {
		return
	}

	handshake, err := noise.NewHandshakeState(config)
	if err != nil {
		return
	}

	indexBytes := packet[:4]
	index := binary.BigEndian.Uint32(indexBytes)
	packet = packet[4:]

	// password was already checked to be 32 bytes. not converted to fixed for convenience
	timestamp, password, err := getTimestampAndPassword(handshake, packet)
	var passwordFixed [32]byte

	copy(passwordFixed[:], password)

	if passwordFixed != s.password {
		return ErrIncorrectPassword
	}

	var pubkey Key
	copy(pubkey[:], handshake.PeerStatic())
	client, ok := s.keyMap[pubkey]
	if !ok {
		client = &Peer{
			pubkey: pubkey,
		}
		s.keyMap[pubkey] = client
		///////////////////////////////////////////////////////////////////////////////
		pubkeyStr := base32.StdEncoding.EncodeToString(pubkey[:])
		updateRecord(pubkeyStr, client.ip.String(), strconv.Itoa(int(client.port)))
	}
	if timestamp <= client.lastHandshake {
		err = ErrOldTimestamp
		return
	}
	client.lastHandshake = timestamp
	// clear old entry
	s.indexMap[index] = nil
	client.ip = clientAddr.IP
	client.port = uint16(clientAddr.Port)
	// if index is aleady taken, set a new one
	for {
		_, ok = s.indexMap[index]
		if !ok {
			break
		}
		index++
	}
	client.index = index
	binary.BigEndian.PutUint32(indexBytes, index)
	s.indexMap[index] = client

	header := append([]byte{PacketHandshakeResp}, indexBytes...)
	// recv and send are opposite order from client code
	packet, recv, send, err := handshake.WriteMessage(header, nil)
	if err != nil {
		return
	}
	client.send = auth.NewCipherState(send.Cipher())
	client.recv = auth.NewCipherState(recv.Cipher())

	_, err = s.conn.WriteTo(packet, clientAddr)

	return
}
