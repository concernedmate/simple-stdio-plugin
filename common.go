package simplestdioplugin

import (
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/google/uuid"
)

type EncodedCommandType uint8

const (
	COMMAND_DATA      EncodedCommandType = 1
	COMMAND_ERROR     EncodedCommandType = 2
	COMMAND_RESULT    EncodedCommandType = 3
	COMMAND_HEARTBEAT EncodedCommandType = 4
	COMMAND_REQUEST   EncodedCommandType = 5
)

type ReadResult struct {
	uuid    []byte
	data    []byte
	command EncodedCommandType
}

// MessageInput is used to route function and provided data
type MessageInput struct {
	Function string
	Data     []byte
}

const PROTOCOL_VERSION uint8 = 5
const PROTOCOL_INFO_SIZE = 44
const PROTOCOL_MAX_SIZE = 4096 // dont change
const CHUNK_SIZE = PROTOCOL_MAX_SIZE - PROTOCOL_INFO_SIZE

// 05 01 0000 id ... 0xAD
// version-command-length-id-data-ending
//
// encodeCommandProtocol is for communication between host application and client plugin
func encodeCommandProtocol(uuid []byte, command_type EncodedCommandType, data []byte) ([]byte, error) {
	if len(uuid) != 36 {
		return nil, fmt.Errorf("invalid uuid length")
	}

	var total int = 2 + 4 + len(uuid) + 1 + len(data) + 1
	if total > PROTOCOL_MAX_SIZE {
		return nil, fmt.Errorf("command too long: %d bytes (max 4096)", total)
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
func writeAll(uuid []byte, data []byte, command_type EncodedCommandType, pipe *os.File) error {
	var chunk []byte
	var counter int
	for {
		if counter >= len(data) {
			break
		}

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

	eof, err := encodeCommandProtocol(uuid, command_type, []byte{})
	if err != nil {
		return err
	}
	_, err = pipe.Write(eof)
	if err != nil {
		return err
	}

	return nil
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
	if command != byte(COMMAND_DATA) && command != byte(COMMAND_ERROR) &&
		command != byte(COMMAND_RESULT) && command != byte(COMMAND_REQUEST) &&
		command != byte(COMMAND_HEARTBEAT) {
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
	resp := response[37:(len(response) - 1)] // +1 separator

	return ReadResult{uuid: id, data: resp, command: EncodedCommandType(command)}, nil
}

func writeHeartbeat(pipe *os.File) error {
	eof, err := encodeCommandProtocol([]byte(uuid.New().String()), COMMAND_HEARTBEAT, []byte{})
	if err != nil {
		return err
	}
	_, err = pipe.Write(eof)
	if err != nil {
		return err
	}

	return nil
}

func encodeMessage(data MessageInput) ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(data); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}
func decodeMessage(data []byte) (MessageInput, error) {
	var input MessageInput

	dec := gob.NewDecoder(bytes.NewBuffer(data))
	if err := dec.Decode(&input); err != nil {
		return MessageInput{}, err
	}

	return input, nil
}
