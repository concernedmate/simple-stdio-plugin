package simplestdioplugin

import (
	"encoding/binary"
	"os"
	"strings"
)

type PluginData struct {
	Router map[string]func(queries map[string]string) ([]byte, error)

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

func parseClientMessage(msg string) (string, map[string]string) {
	if strings.Contains(msg, "?") {
		split := strings.Split(msg, "?")
		sub := split[0]
		queries := strings.Split(split[1], "&")
		mapped := map[string]string{}
		for _, val := range queries {
			query_split := strings.Split(val, "=")
			mapped[query_split[0]] = query_split[1]
		}
		return sub, mapped
	}

	return msg, nil
}

func NewPlugin(Router map[string]func(queries map[string]string) ([]byte, error), Stdin *os.File, Stdout *os.File) PluginData {
	return PluginData{Router: Router, Stdin: Stdin, Stdout: Stdout}
}

func PluginServe(plugin PluginData) error {
	for {
		data, err := plugin.readInput()
		if err != nil {
			return err
		}

		sub, queries := parseClientMessage(string(data))

		function := plugin.Router[sub]
		if function != nil {
			result, err := function(queries)
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
