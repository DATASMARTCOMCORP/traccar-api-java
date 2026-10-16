package model

import (
	"encoding/json"
	"strconv"
)

// Network is the cell and Wi-Fi data of a position (tc_positions.network, JSON text).
// Like the Java class, it omits null fields, writes fields in declaration order, and
// starts with radioType "gsm" and considerIp false.
//
// Java holds the towers and access points in HashSets, so when Java creates a position
// their order follows Java hash codes. Reading and writing back keeps the stored order;
// creating new positions in Java's order is part of porting the decoders (Step 7).
type Network struct {
	HomeMobileCountryCode *int64
	HomeMobileNetworkCode *int64
	RadioType             *string
	Carrier               *string
	ConsiderIP            *bool
	CellTowers            []CellTower       // nil is omitted
	WifiAccessPoints      []WifiAccessPoint // nil is omitted
}

// NewNetwork returns a Network with Java's constructor defaults.
func NewNetwork() *Network {
	gsm, no := "gsm", false
	return &Network{RadioType: &gsm, ConsiderIP: &no}
}

type CellTower struct {
	RadioType         *string
	CellID            *int64
	LocationAreaCode  *int64
	MobileCountryCode *int64
	MobileNetworkCode *int64
	SignalStrength    *int64
}

type WifiAccessPoint struct {
	MacAddress     *string
	SignalStrength *int64
	Channel        *int64
}

// fields writes non-nil values in order, like Jackson with NON_NULL.
type fields struct {
	b     []byte
	first bool
}

func newFields(b []byte) *fields { return &fields{b: append(b, '{'), first: true} }

func (f *fields) key(name string) {
	if !f.first {
		f.b = append(f.b, ',')
	}
	f.first = false
	f.b = appendString(f.b, name)
	f.b = append(f.b, ':')
}

func (f *fields) str(name string, v *string) {
	if v != nil {
		f.key(name)
		f.b = appendString(f.b, *v)
	}
}

func (f *fields) int(name string, v *int64) {
	if v != nil {
		f.key(name)
		f.b = strconv.AppendInt(f.b, *v, 10)
	}
}

func (f *fields) bool(name string, v *bool) {
	if v != nil {
		f.key(name)
		f.b = strconv.AppendBool(f.b, *v)
	}
}

func (f *fields) end() []byte { return append(f.b, '}') }

func (n Network) MarshalJSON() ([]byte, error) { return n.appendJSON(nil), nil }

func (n *Network) appendJSON(b []byte) []byte {
	f := newFields(b)
	f.int("homeMobileCountryCode", n.HomeMobileCountryCode)
	f.int("homeMobileNetworkCode", n.HomeMobileNetworkCode)
	f.str("radioType", n.RadioType)
	f.str("carrier", n.Carrier)
	f.bool("considerIp", n.ConsiderIP)
	if n.CellTowers != nil {
		f.key("cellTowers")
		f.b = append(f.b, '[')
		for i, c := range n.CellTowers {
			if i > 0 {
				f.b = append(f.b, ',')
			}
			t := newFields(f.b)
			t.str("radioType", c.RadioType)
			t.int("cellId", c.CellID)
			t.int("locationAreaCode", c.LocationAreaCode)
			t.int("mobileCountryCode", c.MobileCountryCode)
			t.int("mobileNetworkCode", c.MobileNetworkCode)
			t.int("signalStrength", c.SignalStrength)
			f.b = t.end()
		}
		f.b = append(f.b, ']')
	}
	if n.WifiAccessPoints != nil {
		f.key("wifiAccessPoints")
		f.b = append(f.b, '[')
		for i, w := range n.WifiAccessPoints {
			if i > 0 {
				f.b = append(f.b, ',')
			}
			t := newFields(f.b)
			t.str("macAddress", w.MacAddress)
			t.int("signalStrength", w.SignalStrength)
			t.int("channel", w.Channel)
			f.b = t.end()
		}
		f.b = append(f.b, ']')
	}
	return f.end()
}

// UnmarshalJSON reads a Network. Keys absent from the input keep Java's constructor
// defaults, and positive signal strengths are negated, as CellTower.setSignalStrength does.
func (n *Network) UnmarshalJSON(data []byte) error {
	var in struct {
		HomeMobileCountryCode *int64  `json:"homeMobileCountryCode"`
		HomeMobileNetworkCode *int64  `json:"homeMobileNetworkCode"`
		RadioType             *string `json:"radioType"`
		Carrier               *string `json:"carrier"`
		ConsiderIP            *bool   `json:"considerIp"`
		CellTowers            []struct {
			RadioType         *string `json:"radioType"`
			CellID            *int64  `json:"cellId"`
			LocationAreaCode  *int64  `json:"locationAreaCode"`
			MobileCountryCode *int64  `json:"mobileCountryCode"`
			MobileNetworkCode *int64  `json:"mobileNetworkCode"`
			SignalStrength    *int64  `json:"signalStrength"`
		} `json:"cellTowers"`
		WifiAccessPoints []struct {
			MacAddress     *string `json:"macAddress"`
			SignalStrength *int64  `json:"signalStrength"`
			Channel        *int64  `json:"channel"`
		} `json:"wifiAccessPoints"`
	}
	// Pre-set the defaults; json leaves absent keys untouched and sets explicit nulls.
	defaults := NewNetwork()
	in.RadioType, in.ConsiderIP = defaults.RadioType, defaults.ConsiderIP
	if err := json.Unmarshal(data, &in); err != nil {
		return err
	}
	*n = Network{
		HomeMobileCountryCode: in.HomeMobileCountryCode,
		HomeMobileNetworkCode: in.HomeMobileNetworkCode,
		RadioType:             in.RadioType,
		Carrier:               in.Carrier,
		ConsiderIP:            in.ConsiderIP,
	}
	if in.CellTowers != nil {
		n.CellTowers = make([]CellTower, len(in.CellTowers))
		for i, c := range in.CellTowers {
			n.CellTowers[i] = CellTower{
				RadioType: c.RadioType, CellID: c.CellID, LocationAreaCode: c.LocationAreaCode,
				MobileCountryCode: c.MobileCountryCode, MobileNetworkCode: c.MobileNetworkCode,
				SignalStrength: negative(c.SignalStrength),
			}
		}
	}
	if in.WifiAccessPoints != nil {
		n.WifiAccessPoints = make([]WifiAccessPoint, len(in.WifiAccessPoints))
		for i, w := range in.WifiAccessPoints {
			n.WifiAccessPoints[i] = WifiAccessPoint{MacAddress: w.MacAddress, SignalStrength: w.SignalStrength, Channel: w.Channel}
		}
	}
	return nil
}

func negative(v *int64) *int64 {
	if v != nil && *v > 0 {
		n := -*v
		return &n
	}
	return v
}

// NewCellTowerSet returns an empty HashSet<CellTower>, as Network.addCellTower fills it:
// hashCode is Objects.hash(radioType, cellId, locationAreaCode, mobileCountryCode,
// mobileNetworkCode, signalStrength), with cellId a Long and the others Integers.
func NewCellTowerSet() *JavaSet[CellTower] {
	return NewJavaSet(func(c CellTower) int32 {
		return JavaObjectsHash(stringHash(c.RadioType), longHash(c.CellID), intHash(c.LocationAreaCode),
			intHash(c.MobileCountryCode), intHash(c.MobileNetworkCode), intHash(c.SignalStrength))
	}, func(a, b CellTower) bool {
		return eqPtr(a.RadioType, b.RadioType) && eqPtr(a.CellID, b.CellID) &&
			eqPtr(a.LocationAreaCode, b.LocationAreaCode) && eqPtr(a.MobileCountryCode, b.MobileCountryCode) &&
			eqPtr(a.MobileNetworkCode, b.MobileNetworkCode) && eqPtr(a.SignalStrength, b.SignalStrength)
	})
}

// NewWifiAccessPointSet returns an empty HashSet<WifiAccessPoint>: hashCode is
// Objects.hash(macAddress, signalStrength, channel), the last two Integers.
func NewWifiAccessPointSet() *JavaSet[WifiAccessPoint] {
	return NewJavaSet(func(w WifiAccessPoint) int32 {
		return JavaObjectsHash(stringHash(w.MacAddress), intHash(w.SignalStrength), intHash(w.Channel))
	}, func(a, b WifiAccessPoint) bool {
		return eqPtr(a.MacAddress, b.MacAddress) && eqPtr(a.SignalStrength, b.SignalStrength) && eqPtr(a.Channel, b.Channel)
	})
}

func stringHash(s *string) int32 {
	if s == nil {
		return 0
	}
	return JavaStringHash(*s)
}

func longHash(v *int64) int32 {
	if v == nil {
		return 0
	}
	return JavaLongHash(*v)
}

// intHash is Integer.hashCode for a value Java holds as an Integer.
func intHash(v *int64) int32 {
	if v == nil {
		return 0
	}
	return int32(*v)
}

func eqPtr[T comparable](a, b *T) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

// NewCellTower is CellTower.from(mcc, mnc, lac, cid).
func NewCellTower(mcc, mnc, lac int, cid int64) CellTower {
	return CellTower{MobileCountryCode: ptr(int64(mcc)), MobileNetworkCode: ptr(int64(mnc)),
		LocationAreaCode: ptr(int64(lac)), CellID: &cid}
}

// NewCellTowerRSSI is CellTower.from(mcc, mnc, lac, cid, rssi).
func NewCellTowerRSSI(mcc, mnc, lac int, cid int64, rssi int) CellTower {
	c := NewCellTower(mcc, mnc, lac, cid)
	c.SetSignalStrength(rssi)
	return c
}

// SetSignalStrength is CellTower.setSignalStrength: positive values are negated.
func (c *CellTower) SetSignalStrength(rssi int) { c.SignalStrength = ptr(int64(negate(int32(rssi)))) }

// SetOperator is CellTower.setOperator: the first three decimal digits are the MCC, the
// rest the MNC. Like Java's, it fails on fewer than four digits.
func (c *CellTower) SetOperator(operator int64) error {
	s := strconv.FormatInt(operator, 10)
	if len(s) < 3 {
		return errNumberFormat
	}
	mcc, err := ParseInt(s[:3])
	if err != nil {
		return err
	}
	mnc, err := ParseInt(s[3:])
	if err != nil {
		return err
	}
	c.MobileCountryCode, c.MobileNetworkCode = ptr(int64(mcc)), ptr(int64(mnc))
	return nil
}

// SetSignalStrength is WifiAccessPoint.setSignalStrength: positive values are negated.
func (w *WifiAccessPoint) SetSignalStrength(rssi int) {
	w.SignalStrength = ptr(int64(negate(int32(rssi))))
}

func negate(v int32) int32 {
	if v > 0 {
		return -v
	}
	return v
}

// NewWifiAccessPoint is WifiAccessPoint.from(macAddress, signalStrength).
func NewWifiAccessPoint(mac string, rssi int) WifiAccessPoint {
	w := WifiAccessPoint{MacAddress: &mac}
	w.SetSignalStrength(rssi)
	return w
}

// NewWifiAccessPointChannel is WifiAccessPoint.from(macAddress, signalStrength, channel).
func NewWifiAccessPointChannel(mac string, rssi, channel int) WifiAccessPoint {
	w := NewWifiAccessPoint(mac, rssi)
	w.Channel = ptr(int64(channel))
	return w
}

func ptr[T any](v T) *T { return &v }

// AddCellTower is Network.addCellTower: the towers are a HashSet, so duplicates are
// dropped and CellTowers follows Java's iteration order.
func (n *Network) AddCellTower(c CellTower) {
	set := NewCellTowerSet()
	for _, t := range n.CellTowers {
		set.Add(t)
	}
	set.Add(c)
	n.CellTowers = set.Items()
}

// AddWifiAccessPoint is Network.addWifiAccessPoint (a HashSet, as AddCellTower).
func (n *Network) AddWifiAccessPoint(w WifiAccessPoint) {
	set := NewWifiAccessPointSet()
	for _, t := range n.WifiAccessPoints {
		set.Add(t)
	}
	set.Add(w)
	n.WifiAccessPoints = set.Items()
}

// NetworkOf is new Network(cellTowers...).
func NetworkOf(towers ...CellTower) *Network {
	n := NewNetwork()
	for _, c := range towers {
		n.AddCellTower(c)
	}
	return n
}

// NetworkOfWifi is new Network(wifiAccessPoints...).
func NetworkOfWifi(points ...WifiAccessPoint) *Network {
	n := NewNetwork()
	for _, w := range points {
		n.AddWifiAccessPoint(w)
	}
	return n
}
