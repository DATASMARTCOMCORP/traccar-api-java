package protocol

import "github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"

// SupportedCommands returns the command types a protocol accepts on the data or text
// channel, in the order Java returns them: BaseProtocol copies its declared set into a
// new HashSet and adds "custom", so the order is that HashSet's.
func SupportedCommands(protocol string, text bool) []string {
	declared := declaredCommands[protocol].data
	if text {
		declared = declaredCommands[protocol].text
	}
	set := model.NewHashSet()
	for _, c := range declared {
		set.Add(c)
	}
	copied := model.NewHashSetOf(set.Items())
	copied.Add("custom")
	return copied.Items()
}
