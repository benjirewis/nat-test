package main

import (
	"encoding/hex"
	"net"
	"time"

	"github.com/pion/stun/v3"
	"go.viam.com/rdk/logging"
	"go.viam.com/utils"
)

func main() {
	logger := logging.NewLogger("nat-testing")
	timeout := 10 * time.Second

	if err := testUDP(logger.Sublogger("udp"), timeout); err != nil {
		panic(err)
	}
	if err := testTCP(logger.Sublogger("tcp"), timeout); err != nil {
		panic(err)
	}
}

var (
	stunServerURLsToTestUDP = []string{
		"stun.l.google.com:3478",
		"stun.l.google.com:19302",
		"stun.sipgate.net:3478",
		"stun.sipgate.net:3479",
		"global.stun.twilio.com:3478",
		"turn.viam.com:443",
	}
	stunServerURLsToTestTCP = []string{
		"turn.viam.com:443",
	}
)

func testUDP(logger logging.Logger, timeout time.Duration) error {
	// Listen on arbitrary UDP port.
	conn, err := net.ListenPacket("udp", "0.0.0.0:0")
	if err != nil {
		logger.Warn("Failed to listen over UDP on a port; UDP traffic may be blocked")
		return err
	}
	defer func() {
		utils.UncheckedError(conn.Close())
	}()

	// Set a deadline for all interactions of now + timeout.
	if timeout > 0 {
		if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
			return err
		}
	}

	// Build a STUN binding request to be used against all STUN servers.
	bindRequest, err := stun.Build([]stun.Setter{
		stun.TransactionID,
		stun.BindingRequest,
	}...)
	if err != nil {
		return err
	}
	bindRequestRaw, err := bindRequest.MarshalBinary()
	if err != nil {
		return err
	}

	var stunResponses []*STUNResponse
	for _, stunServerURLToTest := range stunServerURLsToTestUDP {
		logger := logger.WithFields("stun_server_url", stunServerURLToTest)

		stunResponse := NewSTUNResponse(stunServerURLToTest)
		stunResponses = append(stunResponses, stunResponse)

		udpAddr, err := net.ResolveUDPAddr("udp4", stunServerURLToTest)
		if err != nil {
			logger.Errorw("Error resolving URL to a UDP address", "error", err)
			continue
		}
		stunResponse.STUNServerAddr = udpAddr.String()

		// Write bind request on connection to UDP addr.
		bindStart := time.Now()
		n, err := conn.WriteTo(bindRequestRaw, udpAddr)
		if err != nil {
			logger.Errorw("Error writing to conn", "error", err)
			continue
		}
		if n != len(bindRequestRaw) {
			logger.Errorf("Only wrote %d/%d of bind request", n, len(bindRequestRaw))
			continue
		}

		// Receive response from connection.
		rawResponse := make([]byte, 2000 /* arbitrarily large */)
		n, _, err = conn.ReadFrom(rawResponse)
		if err != nil {
			logger.Errorw("Error reading from conn", "error", err)
			continue
		}

		response := &stun.Message{}
		if err := stun.Decode(rawResponse, response); err != nil {
			logger.Errorw("Error decoding STUN message", "error", err)
			continue
		}

		switch c := response.Type.Class; c {
		case stun.ClassSuccessResponse:
			var bindResponseAddr stun.XORMappedAddress
			if err := bindResponseAddr.GetFrom(response); err != nil {
				logger.Errorw("Error extracting address from STUN message", "error", err)
				continue
			}

			// Check for transaction ID mismatch.
			if bindRequest.TransactionID != response.TransactionID {
				logger.Errorf("Transaction ID mismatch (expected %s, got %s)",
					hex.EncodeToString(bindRequest.TransactionID[:]),
					hex.EncodeToString(response.TransactionID[:]),
				)
				continue
			}

			stunResponse.BindResponseAddr = bindResponseAddr.String()
			stunResponse.TimeToBindResponse = time.Since(bindStart)
		default:
			logger.Errorw("Unexpected STUN response received", "response_type", c)
		}
	}

	logSTUNResults(logger, stunResponses)
	return nil
}

func testTCP(logger logging.Logger, timeout time.Duration) error {
	// Create a dialer with a consistent port (randomly chosen) from
	// which to dial over tcp.
	dialer := &net.Dialer{
		Timeout: timeout,
		LocalAddr: &net.TCPAddr{
			IP: net.ParseIP("0.0.0.0"),
		},
	}

	// Build a STUN binding request to be used against all STUN servers.
	bindRequest, err := stun.Build([]stun.Setter{
		stun.TransactionID,
		stun.BindingRequest,
	}...)
	if err != nil {
		return err
	}
	bindRequestRaw, err := bindRequest.MarshalBinary()
	if err != nil {
		return err
	}

	var stunResponses []*STUNResponse
	for _, stunServerURLToTest := range stunServerURLsToTestTCP {
		logger := logger.WithFields("stun_server_url", stunServerURLToTest)

		// Unlike with UDP, TCP needs a new `conn` for every STUN server test (all
		// derived from the same dialer that uses the same local address.)
		conn, err := dialer.Dial("tcp", stunServerURLToTest)
		if err != nil {
			logger.Error("Error dialing STUN server via tcp")
			continue
		}
		defer func() {
			utils.UncheckedError(conn.Close())
		}()

		if timeout > 0 {
			if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
				logger.Error("Error setting read deadline on TCP connection")
				continue
			}
		}

		stunResponse := NewSTUNResponse(stunServerURLToTest)
		stunResponses = append(stunResponses, stunResponse)

		tcpAddr, err := net.ResolveTCPAddr("tcp", stunServerURLToTest)
		if err != nil {
			logger.Errorw("Error resolving URL to a TCP address", "error", err)
			continue
		}
		stunResponse.STUNServerAddr = tcpAddr.String()

		// Write bind request on connection to UDP addr.
		bindStart := time.Now()
		n, err := conn.Write(bindRequestRaw)
		if err != nil {
			logger.Errorw("Error writing to conn", "error", err)
			continue
		}
		if n != len(bindRequestRaw) {
			logger.Errorf("Only wrote %d/%d of bind request", n, len(bindRequestRaw))
			continue
		}

		// Receive response from connection.
		rawResponse := make([]byte, 2000 /* arbitrarily large */)
		n, err = conn.Read(rawResponse)
		if err != nil {
			logger.Errorw("Error reading from conn", "error", err)
			continue
		}

		response := &stun.Message{}
		if err := stun.Decode(rawResponse, response); err != nil {
			logger.Errorw("Error decoding STUN message", "error", err)
			continue
		}

		switch c := response.Type.Class; c {
		case stun.ClassSuccessResponse:
			var bindResponseAddr stun.XORMappedAddress
			if err := bindResponseAddr.GetFrom(response); err != nil {
				logger.Errorw("Error extracting address from STUN message", "error", err)
				continue
			}

			// Check for transaction ID mismatch.
			if bindRequest.TransactionID != response.TransactionID {
				logger.Errorf("Transaction ID mismatch (expected %s, got %s)",
					hex.EncodeToString(bindRequest.TransactionID[:]),
					hex.EncodeToString(response.TransactionID[:]),
				)
				continue
			}

			stunResponse.BindResponseAddr = bindResponseAddr.String()
			stunResponse.TimeToBindResponse = time.Since(bindStart)
		default:
			logger.Errorw("Unexpected STUN response received", "response_type", c)
		}
	}

	logSTUNResults(logger, stunResponses)
	return nil
}
