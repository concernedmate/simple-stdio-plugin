package simplestdioplugin

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
)

type EncodedCommandType uint8

const (
	COMMAND_DATA  EncodedCommandType = 1
	COMMAND_ERROR EncodedCommandType = 2
	COMMAND_FINAL EncodedCommandType = 3
)

type ReadResult struct {
	uuid    []byte
	data    []byte
	command EncodedCommandType
}

const PROTOCOL_VERSION uint8 = 5
const CHUNK_SIZE = 3072

// 05 01 0000 id ... 0xAD
// version-command-length-id-data-ending
//
// encodeCommandProtocol is for communication between host application and client plugin
func encodeCommandProtocol(uuid []byte, command_type EncodedCommandType, data []byte) ([]byte, error) {
	if len(uuid) != 36 {
		return nil, fmt.Errorf("invalid uuid length")
	}

	var total int = 2 + 4 + len(uuid) + 1 + len(data) + 1
	if total > math.MaxInt32 {
		return nil, fmt.Errorf("command too long: %d bytes (max 2147483647)", total)
	}

	result := make([]byte, total)

	result[0] = PROTOCOL_VERSION                                          // version
	result[1] = byte(command_type)                                        // type
	binary.BigEndian.PutUint32(result[2:], uint32(len(uuid)+len(data)+1)) // uuid + data length

	combined := append(uuid, []byte("-")...)
	combined = append(combined, data...)

	result = slices.Replace(result, 6, 6+len(uuid)+len(data)+1, combined...)

	result[len(result)-1] = 0xAD

	return result, nil
}

// WriteAll sends whole data using provided uuid as key for chunking data
func writeAll(uuid []byte, data []byte, pipe *os.File) error {
	counter := 0
	for {
		if counter >= len(data) {
			break
		}

		var chunk []byte
		if counter+CHUNK_SIZE >= len(data) {
			chunk = data[counter:]
		} else {
			chunk = data[counter:(counter + CHUNK_SIZE)]
		}
		counter += CHUNK_SIZE

		total, err := encodeCommandProtocol(uuid, COMMAND_DATA, chunk)
		if err != nil {
			return err
		}
		_, err = pipe.Write(total)
		if err != nil {
			return err
		}
	}

	eof, err := encodeCommandProtocol(uuid, COMMAND_FINAL, []byte{})
	if err != nil {
		return err
	}
	_, err = pipe.Write(eof)
	if err != nil {
		return err
	}

	return nil
}

// ReadAll reads whole data from pipe until EOF this errors if while reading
// there is difference in UUID between chunks
func readAll(pipe *os.File) (ReadResult, error) {
	var data []byte
	var uuid []byte
	for {
		read, err := readChunk(pipe)
		if err != nil {
			return ReadResult{}, err
		}

		switch read.command {
		case COMMAND_ERROR:
			return ReadResult{uuid: read.uuid, data: data, command: read.command}, nil
		case COMMAND_DATA:
			if uuid == nil {
				uuid = read.uuid
			} else {
				if string(uuid) != string(read.uuid) {
					return ReadResult{}, errors.New("difference in uuid between chunk")
				}
			}
			data = append(data, read.data[:len(read.data)-1]...)
		case COMMAND_FINAL:
			return ReadResult{uuid: read.uuid, data: data, command: read.command}, nil
		default:
			return ReadResult{}, errors.New("invalid command")
		}
	}
}

// ReadChunk reads single chunk
func readChunk(pipe *os.File) (ReadResult, error) {
	header := make([]byte, 6)
	n, err := pipe.Read(header)
	if err != nil {
		return ReadResult{}, errors.New("failed to read header: " + err.Error())
	}
	if n != 6 {
		return ReadResult{}, errors.New("invalid header packet length")
	}

	version := header[0]
	command := header[1]
	if version != PROTOCOL_VERSION {
		return ReadResult{}, errors.New("invalid protocol version")
	}
	if command != byte(COMMAND_DATA) && command != byte(COMMAND_ERROR) && command != byte(COMMAND_FINAL) {
		return ReadResult{}, errors.New("invalid protocol command")
	}

	length := binary.BigEndian.Uint32(header[2:])
	// 37 = uuid + separator + end byte
	if length < 37 {
		return ReadResult{}, errors.New("invalid response")
	}

	response := make([]byte, length+1)
	n, err = pipe.Read(response)
	if err != nil {
		return ReadResult{}, errors.New("failed to read data: " + err.Error())
	}
	if n != int(length)+1 {
		return ReadResult{}, errors.New("invalid data packet length")
	}

	id := response[0:36]
	resp := response[37:] // +1 separator

	return ReadResult{uuid: id, data: resp, command: EncodedCommandType(command)}, nil
}

// MessageInput is used to route function and provided data
type MessageInput struct {
	Function string
	Data     []byte
}

func encodeMessage(data MessageInput) ([]byte, error) {
	result := map[string]any{
		"Function": data.Function,
		"Data":     base64.StdEncoding.EncodeToString(data.Data),
	}

	return json.Marshal(result)
}
func decodeMessage(data []byte) (MessageInput, error) {
	var input map[string]string

	if err := json.Unmarshal(data, &input); err != nil {
		return MessageInput{}, err
	}

	decoded, err := base64.StdEncoding.DecodeString(input["Data"])
	if err != nil {
		return MessageInput{}, err
	}

	return MessageInput{Function: input["Function"], Data: decoded}, nil
}
