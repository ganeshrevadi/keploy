package postgresparser

import (
	"encoding/base64"

	// "fmt"

	"encoding/binary"
	"errors"
	"unicode"

	"github.com/agnivade/levenshtein"
	"go.keploy.io/server/pkg/hooks"
	"go.keploy.io/server/pkg/models"
	"go.uber.org/zap"
)

func remainingBits(superset, subset []byte) []byte {
	// Find the length of the smaller array (subset).
	subsetLen := len(subset)

	// Initialize a result buffer to hold the differences.
	var difference []byte

	// Iterate through each byte in the 'subset' array.
	for i := 0; i < subsetLen; i++ {
		// Compare the bytes at the same index in both arrays.
		// If they are different, append the byte from 'superset' to the result buffer.
		if superset[i] != subset[i] {
			difference = append(difference, superset[i])
		}
	}

	// If 'superset' is longer than 'subset', append the remaining bytes to the result buffer.
	if len(superset) > subsetLen {
		difference = append(difference, superset[subsetLen:]...)
	}

	return difference
}

func PostgresDecoder(encoded string) ([]byte, error) {
	// decode the base 64 encoded string to buffer ..

	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		// fmt.Println(Emoji+"failed to decode the data", err)
		return nil, err
	}
	// println("Decoded data is :", string(data))
	return data, nil
}

func PostgresEncoder(buffer []byte) string {
	// encode the buffer to base 64 string ..
	encoded := base64.StdEncoding.EncodeToString(buffer)
	return encoded
}

func IdentifyPacket(data []byte) (models.Packet, error) {
	// At least 4 bytes are required to determine the length
	if len(data) < 4 {
		return nil, errors.New("data too short")
	}

	// Read the length (first 32 bits)
	length := binary.BigEndian.Uint32(data[:4])

	// If the length is followed by the protocol version, it's a StartupPacket
	if length > 4 && len(data) >= int(length) && binary.BigEndian.Uint32(data[4:8]) == models.ProtocolVersionNumber {
		return &models.StartupPacket{
			Length:          length,
			ProtocolVersion: binary.BigEndian.Uint32(data[4:8]),
		}, nil
	}

	// If we have an ASCII identifier, then it's likely a regular packet. Further validations can be added.
	if len(data) > 5 && len(data) >= int(length)+1 {
		return &models.RegularPacket{
			Identifier: data[4],
			Length:     length,
			Payload:    data[5 : length+1],
		}, nil
	}

	return nil, errors.New("unknown packet type or data too short for declared length")
}


type Request struct {
	Data     []byte
	IsString bool
}

func findStringMatch(req string, mockString []string) int {
	minDist := int(^uint(0) >> 1) // Initialize with max int value
	bestMatch := -1
	for idx, req := range mockString {
		if !IsAsciiPrintable(mockString[idx]) {
			continue
		}

		dist := levenshtein.ComputeDistance(req, mockString[idx])
		if dist == 0 {
			return 0
		}

		if dist < minDist {
			minDist = dist
			bestMatch = idx
		}
	}
	return bestMatch
}

func IsAsciiPrintable(s string) bool {
	for _, r := range s {
		if r > unicode.MaxASCII || !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}

func Fuzzymatch(configMocks, tcsMocks []*models.Mock, reqBuff []byte, h *hooks.Hook) (bool, string) {
	com := PostgresEncoder(reqBuff)
	for idx, mock := range tcsMocks {
		encoded, _ := PostgresDecoder(mock.Spec.PostgresReq.Payload)

		if string(encoded) == string(reqBuff) || mock.Spec.PostgresReq.Payload == com {
			// fmt.Println("matched in first loop")
			tcsMocks = append(tcsMocks[:idx], tcsMocks[idx+1:]...)
			h.SetTcsMocks(tcsMocks)
			return true, mock.Spec.PostgresResp.Payload
		}
	}
	// convert all the configmocks to string array
	mockString := make([]string, len(tcsMocks))
	for i := 0; i < len(tcsMocks); i++ {
		mockString[i] = string(tcsMocks[i].Spec.PostgresReq.Payload)
	}
	// find the closest match
	if IsAsciiPrintable(string(reqBuff)) {
		// fmt.Println("Inside String Match")
		idx := findStringMatch(string(reqBuff), mockString)
		if idx != -1 {
			nMatch := tcsMocks[idx].Spec.PostgresResp.Payload
			tcsMocks = append(tcsMocks[:idx], tcsMocks[idx+1:]...)
			h.SetTcsMocks(tcsMocks)
			// fmt.Println("Returning mock from String Match !!")
			return true, nMatch
		}
	}
	idx := findBinaryMatch(tcsMocks, reqBuff, h)
	if idx != -1 {
		nMatch := tcsMocks[idx].Spec.PostgresResp.Payload
		tcsMocks = append(tcsMocks[:idx], tcsMocks[idx+1:]...)
		h.SetTcsMocks(tcsMocks)
		return true, nMatch
	}
	return false, ""
}

func AdaptiveK(length, kMin, kMax, N int) int {
	k := length / N
	if k < kMin {
		return kMin
	} else if k > kMax {
		return kMax
	}
	return k
}

func matchingPg(tcsMocks []*models.Mock, requestBuffers [][]byte, h *hooks.Hook) (bool, []models.GenericPayload) {

	for idx, mock := range tcsMocks {
		if mock == nil {
			continue
		}
		// println("Inside findBinaryMatch", len(mock.Spec.GenericRequests), len(requestBuffers))
		if len(mock.Spec.GenericRequests) == len(requestBuffers) {
			for requestIndex, reqBuff := range requestBuffers {
				bufStr := base64.StdEncoding.EncodeToString(reqBuff)
				encoded, _ := PostgresDecoder(mock.Spec.GenericRequests[requestIndex].Message[0].Data)

				if string(encoded) == string(reqBuff) || mock.Spec.GenericRequests[requestIndex].Message[0].Data == bufStr {
					// fmt.Println("matched in first loop")
					tcsMocks = append(tcsMocks[:idx], tcsMocks[idx+1:]...)
					h.SetTcsMocks(tcsMocks)
					return true, mock.Spec.GenericResponses
				}
			}
		}
	}

	idx := findBinaryStreamMatch(tcsMocks, requestBuffers, h)
	if idx != -1 {
		// fmt.Println("matched in first loop")
		bestMatch := tcsMocks[idx].Spec.GenericResponses
		// println("Lenght of tcsMocks", len(tcsMocks), " BestMatch -->", tcsMocks[idx].Spec.GenericRequests[0].Message[0].Data)
		tcsMocks = append(tcsMocks[:idx], tcsMocks[idx+1:]...)
		h.SetTcsMocks(tcsMocks)
		return true, bestMatch
	}
	return false, nil
}

func findBinaryMatch(configMocks []*models.Mock, reqBuff []byte, h *hooks.Hook) int {

	mxSim := -1.0
	mxIdx := -1
	// find the fuzzy hash of the mocks
	for idx, mock := range configMocks {
		encoded, _ := PostgresDecoder(mock.Spec.PostgresReq.Payload)
		k := AdaptiveK(len(reqBuff), 3, 8, 5)
		shingles1 := CreateShingles(encoded, k)
		shingles2 := CreateShingles(reqBuff, k)
		similarity := JaccardSimilarity(shingles1, shingles2)
		// fmt.Printf("Jaccard Similarity: %f\n", similarity)
		if mxSim < similarity {
			mxSim = similarity
			mxIdx = idx
		}
	}
	return mxIdx
}

func findBinaryStreamMatch(tcsMocks []*models.Mock, requestBuffers [][]byte, h *hooks.Hook) int {

	mxSim := -1.0
	mxIdx := -1

	for idx, mock := range tcsMocks {
		// println("Inside findBinaryMatch", len(mock.Spec.GenericRequests), len(requestBuffers))
		if len(mock.Spec.GenericRequests) == len(requestBuffers) {
			for requestIndex, reqBuff := range requestBuffers {

				// bufStr := string(reqBuff)
				// if !IsAsciiPrintable(bufStr) {
				_ = base64.StdEncoding.EncodeToString(reqBuff)
				// }
				encoded, _ := PostgresDecoder(mock.Spec.GenericRequests[requestIndex].Message[0].Data)

				k := AdaptiveK(len(reqBuff), 3, 8, 5)
				shingles1 := CreateShingles(encoded, k)
				shingles2 := CreateShingles(reqBuff, k)
				similarity := JaccardSimilarity(shingles1, shingles2)
				// fmt.Printf("Jaccard Similarity: %f\n", similarity)
				if mxSim < similarity {
					mxSim = similarity
					mxIdx = idx
				}
			}
		}
	}
	return mxIdx
}

// CreateShingles produces a set of k-shingles from a byte buffer.
func CreateShingles(data []byte, k int) map[string]struct{} {
	shingles := make(map[string]struct{})
	for i := 0; i < len(data)-k+1; i++ {
		shingle := string(data[i : i+k])
		shingles[shingle] = struct{}{}
	}
	return shingles
}

// JaccardSimilarity computes the Jaccard similarity between two sets of shingles.
func JaccardSimilarity(setA, setB map[string]struct{}) float64 {
	intersectionSize := 0
	for k := range setA {
		if _, exists := setB[k]; exists {
			intersectionSize++
		}
	}

	unionSize := len(setA) + len(setB) - intersectionSize

	if unionSize == 0 {
		return 0.0
	}
	return float64(intersectionSize) / float64(unionSize)
}

func ChangeAuthToMD5(tcsMocks []*models.Mock, h *hooks.Hook, log *zap.Logger) {
	// isScram := false
	for _, mock := range tcsMocks {
		// if len(mock.Spec.GenericRequests) == len(requestBuffers) {
		for requestIndex, reqBuff := range mock.Spec.GenericRequests {
			encode, _ := PostgresDecoder(reqBuff.Message[0].Data)

			if isStartupPacket(encode) || checkScram(mock.Spec.GenericResponses[requestIndex].Message[0].Data, log) { //||reqBuff.Message[0].Data == "AAAAeAADAAB1c2VyAGtlcGxveS11c2VyAGRhdGFiYXNlAGtlcGxveS10ZXN0AGNsaWVudF9lbmNvZGluZwBVVEY4AERhdGVTdHlsZQBJU08AVGltZVpvbmUARXRjL1VUQwBleHRyYV9mbG9hdF9kaWdpdHMAMgAA" {

				log.Debug("CHANGING TO MD5 for Response")
				mock.Spec.GenericResponses[requestIndex].Message[0].Data = "UgAAAAwAAAAF4I8BHg=="
				// isScram = true
				continue
			}
			//just change it to more robust and after that write decoode encode logic for all
			if encode[0] == 'p' {
				log.Debug("CHANGING TO MD5 for Request and Response")
				mock.Spec.GenericRequests[requestIndex].Message[0].Data = "cAAAAChtZDUzNTc3MWY3N2YxMDA4YmEzMDRkYjlkMmJmODM3YmZlOQA="
				mock.Spec.GenericResponses[requestIndex].Message[0].Data = "UgAAAAgAAAAAUwAAABZhcHBsaWNhdGlvbl9uYW1lAABTAAAAGWNsaWVudF9lbmNvZGluZwBVVEY4AFMAAAAXRGF0ZVN0eWxlAElTTywgTURZAFMAAAAZaW50ZWdlcl9kYXRldGltZXMAb24AUwAAABtJbnRlcnZhbFN0eWxlAHBvc3RncmVzAFMAAAAUaXNfc3VwZXJ1c2VyAG9uAFMAAAAZc2VydmVyX2VuY29kaW5nAFVURjgAUwAAADJzZXJ2ZXJfdmVyc2lvbgAxMy41IChEZWJpYW4gMTMuNS0xLnBnZGcxMTArMSkAUwAAACNzZXNzaW9uX2F1dGhvcml6YXRpb24AcG9zdGdyZXMAUwAAACNzdGFuZGFyZF9jb25mb3JtaW5nX3N0cmluZ3MAb24AUwAAABVUaW1lWm9uZQBFdGMvVVRDAEsAAAAMAAAAX09sZl9aAAAABUk="
				continue
			}
		}
	}
	h.SetTcsMocks(tcsMocks)
}
