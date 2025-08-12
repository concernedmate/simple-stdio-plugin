package simplestdioplugin

import (
	"encoding/binary"
	"os"
	"strings"
)

type PluginData struct {
	Router map[string]func(json string) ([]byte, error)

	Stdin  *os.File
	Stdout *os.File
}

func (plugin *PluginData) readInput() ([]byte, error) {
	header := make([]byte, 3)
	if _, err := plugin.Stdin.Read(header); err != nil {
		return nil, err
	}

	// version := header[0]
	length := int(binary.BigEndian.Uint16(header[1:]))

	data := make([]byte, length+1) // plus ending
	if _, err := plugin.Stdin.Read(data); err != nil {
		return nil, err
	}
	data = data[:len(data)-1]

	return data, nil
}

func (plugin *PluginData) writeOutput(data []byte) error {
	total, err := EncodeCommand(data)
	if err != nil {
		return err
	}
	plugin.Stdout.Write(total)

	return nil
}

func parseClientMessage(msg string) (sub string, json string) {
	if strings.Contains(msg, "?json=") {
		split := strings.Split(msg, "?json=")

		return split[0], strings.Join(split[1:], "?json=")
	}

	return msg, ""
}

func NewPlugin(Router map[string]func(json string) ([]byte, error), Stdin *os.File, Stdout *os.File) PluginData {
	return PluginData{Router: Router, Stdin: Stdin, Stdout: Stdout}
}

func PluginServe(plugin PluginData) error {
	for {
		data, err := plugin.readInput()
		if err != nil {
			return err
		}

		sub, json := parseClientMessage(string(data))

		function := plugin.Router[sub]
		if function != nil {
			result, err := function(json)
			if err != nil {
				_ = plugin.writeOutput([]byte("plugin error: " + err.Error()))
			} else {
				if err := plugin.writeOutput(result); err != nil {
					plugin.writeOutput([]byte("plugin error: " + err.Error()))
				}
			}
		} else {
			plugin.writeOutput([]byte("plugin error: not found"))
		}
	}
}
