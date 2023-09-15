package postgresparser

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	"go.uber.org/zap"
)

func checkScram(packet string, log *zap.Logger) bool {
	encoded, err := PostgresDecoder(packet)
	if err != nil {
		log.Error("error in decoding packet", zap.Error(err))
		return false
	}
	// check if payload contains SCRAM-SHA-256
	messageType := encoded[0]
	log.Debug("Message Type: %c\n", zap.String("messageType", string(messageType)))
	if messageType == 'N' {
		return false
	}
	// Print the message payload (for simplicity, the payload is printed as a string)
	payload := string(encoded[5:])
	// fmt.Printf("Payload: %s\n", payload)
	if messageType == 'R' {
		// send this payload to get decode the auth type
		err := findAuthenticationMessageType(encoded[5:], log)
		if err != nil {
			log.Error("error in finding authentication message type", zap.Error(err))
			return false
		}
		if strings.Contains(payload, "SCRAM-SHA") {
			log.Debug("scram packet")
			return true
		}
	}

	return false
}

func isStartupPacket(packet []byte) bool {
	protocolVersion := binary.BigEndian.Uint32(packet[4:8])
	return protocolVersion == 196608 // 3.0 in PostgreSQL
}

func isRegularPacket(packet []byte) bool {
	messageType := packet[0]
	return messageType == 'Q' || messageType == 'P' || messageType == 'D' || messageType == 'C' || messageType == 'E'
}

func printStartupPacketDetails(packet []byte) {
	// fmt.Printf("Protocol Version: %d\n", binary.BigEndian.Uint32(packet[4:8]))

	// Print key-value pairs (for simplicity, only one key-value pair is shown)
	keyStart := 8
	for keyStart < len(packet) && packet[keyStart] != 0 {
		keyEnd := keyStart
		for keyEnd < len(packet) && packet[keyEnd] != 0 {
			keyEnd++
		}
		key := string(packet[keyStart:keyEnd])

		valueStart := keyEnd + 1
		valueEnd := valueStart
		for valueEnd < len(packet) && packet[valueEnd] != 0 {
			valueEnd++
		}
		value := string(packet[valueStart:valueEnd])

		fmt.Printf("Key: %s, Value: %s\n", key, value)

		keyStart = valueEnd + 1
	}
}

func printRegularPacketDetails(packet []byte) {
	messageType := packet[0]
	fmt.Printf("Message Type: %c\n", messageType)

	// Print the message payload (for simplicity, the payload is printed as a string)
	payload := string(packet[5:])
	fmt.Printf("Payload: %s\n", payload)
}

func decodeBuffer(buffer []byte) (*PSQLMessage, error) {
	if len(buffer) < 6 {
		return nil, errors.New("invalid buffer length")
	}

	psqlMessage := &PSQLMessage{
		Field1: "test",
		Field2: 123,
	}

	// Decode the ID (4 bytes)
	psqlMessage.ID = binary.BigEndian.Uint32(buffer[:4])

	// Decode the payload length (2 bytes)
	payloadLength := binary.BigEndian.Uint16(buffer[4:6])

	// Check if the buffer contains the full payload
	if len(buffer[6:]) < int(payloadLength) {
		return nil, errors.New("incomplete payload in buffer")
	}

	// Extract the payload from the buffer
	psqlMessage.Payload = buffer[6 : 6+int(payloadLength)]

	return psqlMessage, nil
}

const (
	AuthTypeOk                = 0
	AuthTypeCleartextPassword = 3
	AuthTypeMD5Password       = 5
	AuthTypeSCMCreds          = 6
	AuthTypeGSS               = 7
	AuthTypeGSSCont           = 8
	AuthTypeSSPI              = 9
	AuthTypeSASL              = 10
	AuthTypeSASLContinue      = 11
	AuthTypeSASLFinal         = 12
)

func findAuthenticationMessageType(src []byte,log *zap.Logger ) error {
	// constants.
	if len(src) < 4 {
		return errors.New("authentication message too short")
	}
	authType := binary.BigEndian.Uint32(src[:4])

	switch authType {
	case AuthTypeOk:
		log.Debug("AuthTypeOk")
		return nil
	case AuthTypeCleartextPassword:
		log.Debug("AuthTypeCleartextPassword")
		return nil
	case AuthTypeMD5Password:
		log.Debug("AuthTypeMD5Password")
		return nil
	case AuthTypeSCMCreds:
		log.Debug("AuthTypeSCMCreds")
		return errors.New("AuthTypeSCMCreds is unimplemented")
	case AuthTypeGSS:
		return nil
	case AuthTypeGSSCont:
		return nil
	case AuthTypeSSPI:
		return errors.New("AuthTypeSSPI is unimplemented")
	case AuthTypeSASL:
		log.Debug("AuthTypeSASL")
		DecodeSASL(src,log)
		return nil
	case AuthTypeSASLContinue:
		log.Debug("AuthTypeSASLContinue")

		return nil
	case AuthTypeSASLFinal:
		log.Debug("AuthTypeSASLFinal")
		return nil
	default:
		return fmt.Errorf("unknown authentication type: %d", authType)
	}
}

func DecodeSASL(src []byte, log *zap.Logger) error {
	var AuthMechanisms []string
	log.Debug("AuthenticationSASL.Decode")
	if len(src) < 4 {
		return errors.New("authentication message too short")
	}

	authType := binary.BigEndian.Uint32(src)
	// log.Debug("authType: ", authType)
	if authType != AuthTypeSASL {
		return errors.New("bad auth type")
	}

	authMechanisms := src[4:]
	for len(authMechanisms) > 1 {
		idx := bytes.IndexByte(authMechanisms, 0)
		if idx > 0 {
			AuthMechanisms = append(AuthMechanisms, string(authMechanisms[:idx]))
			authMechanisms = authMechanisms[idx+1:]
		}
	}
	// println("AuthMechanisms: ", AuthMechanisms[0], AuthMechanisms[1])

	return nil
}
