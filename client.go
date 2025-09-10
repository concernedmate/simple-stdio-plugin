package simplestdioplugin

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"log"
	"os"
	"strings"
	"sync"
)

const CHUNK_SIZE = 2048

type PluginData struct {
	Router map[string]func(json []byte) ([]byte, error)

	Stdin  *os.File
	Stdout *os.File
}

func (plugin *PluginData) readInput() (id []byte, data []byte, err error) {
	header := make([]byte, 6)
	if _, err := plugin.Stdin.Read(header); err != nil {
		return nil, nil, err
	}
	// version := header[0]
	// command := header[1]
	length := binary.BigEndian.Uint32(header[2:])

	// 37 = uuid + separator + end byte
	if length < 37 {
		return nil, nil, errors.New("invalid response")
	}

	response := make([]byte, length+1) // plus ending
	_, err = plugin.Stdin.Read(response)
	if err != nil {
		return nil, nil, err
	}

	id = response[0:36]
	data = response[37:] // +1 separator

	data = data[:len(data)-1]

	return id, data, nil
}

func (plugin *PluginData) writeOutput(id []byte, data []byte) error {
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

		total, err := EncodeCommand(id, COMMAND_DATA, chunk)
		if err != nil {
			return err
		}
		_, err = plugin.Stdout.Write(total)
		if err != nil {
			return err
		}
	}

	eof, err := EncodeCommand(id, COMMAND_DATA, []byte{})
	if err != nil {
		return err
	}
	_, err = plugin.Stdout.Write(eof)
	if err != nil {
		return err
	}

	return nil
}

func (plugin *PluginData) writeError(id []byte, data string) error {
	if len([]byte(data)) > CHUNK_SIZE {
		log.Fatal("error message size is over chunk size")
	}

	total, err := EncodeCommand(id, COMMAND_ERROR, []byte(data))
	if err != nil {
		return err
	}
	_, err = plugin.Stdout.Write(total)
	if err != nil {
		return err
	}

	return nil
}

func parseClientMessage(msg string) (sub string, jsondata []byte) {
	if strings.Contains(msg, "?json=") {
		split := strings.Split(msg, "?json=")

		sub = split[0]
		jsondata = []byte(strings.Join(split[1:], "?json="))
		if json.Valid(jsondata) {
			return sub, jsondata
		}

		return sub, nil
	}

	return msg, nil
}

func NewPluginClient(Router map[string]func(json []byte) ([]byte, error), Stdin *os.File, Stdout *os.File) PluginData {
	return PluginData{Router: Router, Stdin: Stdin, Stdout: Stdout}
}

func PluginServe(plugin PluginData, max_concurrent int) error {
	mut := sync.RWMutex{}
	concurrent := 0

	for {
		id, data, err := plugin.readInput()
		if err != nil {
			return err
		}

		mut.RLock()
		curr_req := concurrent
		mut.RUnlock()

		if curr_req >= max_concurrent {
			_ = plugin.writeError(id, "plugin error: MAX CONCURRENT command reached")
			continue
		}

		sub, json := parseClientMessage(string(data))

		mut.Lock()
		concurrent++
		mut.Unlock()
		go func() {
			defer func() {
				mut.Lock()
				concurrent--
				mut.Unlock()
			}()

			function := plugin.Router[sub]
			if function != nil {
				result, err := function(json)
				if err != nil {
					_ = plugin.writeError(id, "plugin error: "+err.Error())
				} else {
					if err := plugin.writeOutput(id, result); err != nil {
						_ = plugin.writeError(id, "plugin error: "+err.Error())
					}
				}
			} else {
				_ = plugin.writeError(id, "plugin error: "+sub+" not found")
			}
		}()
	}
}
