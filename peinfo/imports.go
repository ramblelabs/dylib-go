// Package peinfo inspects PE image import metadata without loading code or DLLs.
package peinfo

// Imports contains the three import directories selected by a PE optional header.
type Imports struct {
	Directories []ImportDirectory `json:"directories,omitempty"`
	Bound       []BoundImport     `json:"bound,omitempty"`
	Delay       []DelayImport     `json:"delay,omitempty"`
}

// Symbol keeps a lookup identity and the original on-disk IAT word separately.
// Name/Ordinal are unavailable when a bound directory has no lookup table.
type Symbol struct {
	Name    string `json:"name,omitempty"`
	Hint    uint16 `json:"hint,omitempty"`
	Ordinal uint16 `json:"ordinal,omitempty"`
	Address uint64 `json:"address"`
}

// ImportDirectory preserves the descriptor and its on-disk address slots.
// LookupAvailable is false for a bound IAT with no separate lookup table.
type ImportDirectory struct {
	DLL             string   `json:"dll"`
	TimeDateStamp   uint32   `json:"timestamp"`
	ForwarderChain  uint32   `json:"forwarder_chain"`
	LookupRVA       uint32   `json:"lookup_rva"`
	AddressRVA      uint32   `json:"address_rva"`
	LookupAvailable bool     `json:"lookup_available"`
	Symbols         []Symbol `json:"symbols,omitempty"`
}

// BoundImport records one bound module and its forwarder references.
type BoundImport struct {
	DLL           string           `json:"dll"`
	TimeDateStamp uint32           `json:"timestamp"`
	Forwarders    []BoundForwarder `json:"forwarders,omitempty"`
}

// BoundForwarder preserves a referenced module's timestamp and reserved word.
type BoundForwarder struct {
	DLL           string `json:"dll"`
	TimeDateStamp uint32 `json:"timestamp"`
	Reserved      uint16 `json:"reserved,omitempty"`
}

// DelayImport reports normalized RVAs even for legacy VA-based descriptors.
// Inspect does not invoke helpers, bind imports, or change the recorded words.
type DelayImport struct {
	DLL              string   `json:"dll"`
	Attributes       uint32   `json:"attributes"`
	TimeDateStamp    uint32   `json:"timestamp"`
	ModuleHandleRVA  uint32   `json:"module_handle_rva"`
	AddressRVA       uint32   `json:"address_rva"`
	NameTableRVA     uint32   `json:"name_table_rva"`
	BoundAddressRVA  uint32   `json:"bound_address_rva,omitempty"`
	UnloadAddressRVA uint32   `json:"unload_address_rva,omitempty"`
	Symbols          []Symbol `json:"symbols,omitempty"`
	BoundAddresses   []uint64 `json:"bound_addresses,omitempty"`
	UnloadAddresses  []uint64 `json:"unload_addresses,omitempty"`
}
