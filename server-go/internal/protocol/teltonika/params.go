package teltonika

import (
	"regexp"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// parameter is a registered IO element: decoded by decode when the device model matches
// (a model of "" is Java's null and matches only any).
type parameter struct {
	match  func(deviceModel string) bool
	decode func(p *model.Position, b *kit.Buf)
}

var (
	fmbXXXPattern = regexp.MustCompile(`^(?:FM[B-Z]...|MTB100|MSP500)$`)
	tatXXXPattern = regexp.MustCompile(`^T.T...$`)

	anyModel = func(string) bool { return true }
	fmbXXX   = fmbXXXPattern.MatchString
	fmb6XX   = fm6xx.MatchString
	tatXXX   = tatXXXPattern.MatchString
	fmbNot6  = func(m string) bool { return fmbXXX(m) && !fmb6XX(m) }
	fmbOrTat = func(m string) bool { return fmbXXX(m) || tatXXX(m) }
)

func alarmIf(alarm string) func(p *model.Position, b *kit.Buf) {
	return func(p *model.Position, b *kit.Buf) {
		if b.ReadUnsignedByte() > 0 {
			p.AddAlarm(alarm)
		}
	}
}

func flag(key string) func(p *model.Position, b *kit.Buf) {
	return func(p *model.Position, b *kit.Buf) { p.Set(key, b.ReadUnsignedByte() > 0) }
}

func unsignedByte(key string) func(p *model.Position, b *kit.Buf) {
	return func(p *model.Position, b *kit.Buf) { p.Set(key, b.ReadUnsignedByte()) }
}

func unsignedShort(key string) func(p *model.Position, b *kit.Buf) {
	return func(p *model.Position, b *kit.Buf) { p.Set(key, b.ReadUnsignedShort()) }
}

func unsignedInt(key string) func(p *model.Position, b *kit.Buf) {
	return func(p *model.Position, b *kit.Buf) { p.Set(key, b.ReadUnsignedInt()) }
}

func signedShort(key string) func(p *model.Position, b *kit.Buf) {
	return func(p *model.Position, b *kit.Buf) { p.Set(key, b.ReadShort()) }
}

func unsignedShortScaled(key string, divisor float64) func(p *model.Position, b *kit.Buf) {
	return func(p *model.Position, b *kit.Buf) { p.Set(key, float64(b.ReadUnsignedShort())/divisor) }
}

func signedShortScaled(key string, divisor float64) func(p *model.Position, b *kit.Buf) {
	return func(p *model.Position, b *kit.Buf) { p.Set(key, float64(b.ReadShort())/divisor) }
}

func unsignedIntScaled(key string, divisor float64) func(p *model.Position, b *kit.Buf) {
	return func(p *model.Position, b *kit.Buf) { p.Set(key, float64(b.ReadUnsignedInt())/divisor) }
}

func intScaled(key string, divisor float64) func(p *model.Position, b *kit.Buf) {
	return func(p *model.Position, b *kit.Buf) { p.Set(key, float64(b.ReadInt())/divisor) }
}

// parameters is TeltonikaProtocolDecoder.PARAMETERS (each id has a single entry).
var parameters = map[int]parameter{
	1:  {anyModel, flag(model.PrefixIn + "1")},
	2:  {anyModel, flag(model.PrefixIn + "2")},
	3:  {anyModel, flag(model.PrefixIn + "3")},
	4:  {anyModel, flag(model.PrefixIn + "4")},
	9:  {fmbXXX, unsignedShortScaled(model.PrefixAdc+"1", 1000.0)},
	10: {fmbXXX, unsignedShortScaled(model.PrefixAdc+"2", 1000.0)},
	11: {fmbXXX, func(p *model.Position, b *kit.Buf) { p.Set(model.KeyIccid, kit.Format("%d", b.ReadLong())) }},
	12: {fmbXXX, unsignedIntScaled(model.KeyFuelUsed, 1000.0)},
	13: {fmbXXX, unsignedShortScaled(model.KeyFuelConsumption, 100.0)},
	16: {anyModel, unsignedInt(model.KeyOdometer)},
	17: {anyModel, signedShort("axisX")},
	18: {anyModel, signedShort("axisY")},
	19: {anyModel, signedShort("axisZ")},
	21: {anyModel, unsignedByte(model.KeyRssi)},
	24: {fmbXXX, func(p *model.Position, b *kit.Buf) { p.Speed = units.KnotsFromKph(float64(b.ReadUnsignedShort())) }},
	25: {anyModel, signedShortScaled("bleTemp1", 100.0)},
	26: {anyModel, signedShortScaled("bleTemp2", 100.0)},
	27: {anyModel, signedShortScaled("bleTemp3", 100.0)},
	28: {anyModel, signedShortScaled("bleTemp4", 100.0)},
	30: {fmbXXX, unsignedByte("faultCount")},
	31: {fmbXXX, unsignedByte(model.KeyEngineLoad)},
	32: {fmbXXX, func(p *model.Position, b *kit.Buf) { p.Set(model.KeyCoolantTemp, int(b.ReadSignedByte())) }},
	36: {fmbXXX, unsignedShort(model.KeyRpm)},
	43: {fmbXXX, unsignedShort("milDistance")},
	57: {fmbXXX, func(p *model.Position, b *kit.Buf) { p.Set("hybridBatteryLevel", int(b.ReadSignedByte())) }},
	66: {anyModel, unsignedShortScaled(model.KeyPower, 1000.0)},
	67: {anyModel, unsignedShortScaled(model.KeyBattery, 1000.0)},
	68: {fmbXXX, unsignedShortScaled("batteryCurrent", 1000.0)},
	72: {fmbXXX, intScaled(model.PrefixTemp+"1", 10.0)},
	73: {fmbXXX, intScaled(model.PrefixTemp+"2", 10.0)},
	74: {fmbXXX, intScaled(model.PrefixTemp+"3", 10.0)},
	75: {fmbXXX, intScaled(model.PrefixTemp+"4", 10.0)},
	78: {anyModel, func(p *model.Position, b *kit.Buf) {
		if driverUniqueID := b.ReadLongLE(); driverUniqueID != 0 {
			p.Set(model.KeyDriverUniqueID, kit.Format("%016X", driverUniqueID))
		}
	}},
	80:  {fmbNot6, unsignedByte("dataMode")},
	81:  {fmbNot6, unsignedByte(model.KeyObdSpeed)},
	82:  {fmbNot6, unsignedByte(model.KeyThrottle)},
	83:  {fmbNot6, unsignedIntScaled(model.KeyFuelUsed, 10.0)},
	84:  {fmbNot6, unsignedShortScaled(model.KeyFuel, 10.0)},
	85:  {fmbNot6, unsignedShort(model.KeyRpm)},
	87:  {fmbNot6, unsignedInt(model.KeyObdOdometer)},
	89:  {fmbNot6, unsignedByte(model.KeyFuelLevel)},
	107: {fmbXXX, unsignedIntScaled(model.KeyFuelUsed, 10.0)},
	110: {fmbXXX, unsignedShortScaled(model.KeyFuelConsumption, 10.0)},
	113: {fmbXXX, unsignedByte(model.KeyBatteryLevel)},
	115: {fmbXXX, signedShortScaled(model.KeyEngineTemp, 10.0)},
	389: {func(m string) bool { return m == "FMB003" }, func(p *model.Position, b *kit.Buf) {
		p.Set(model.KeyObdOdometer, b.ReadUnsignedInt()*1000)
	}},
	701: {fmb6XX, signedShortScaled("bleTemp1", 10.0)},
	702: {fmb6XX, signedShortScaled("bleTemp2", 10.0)},
	703: {fmb6XX, signedShortScaled("bleTemp3", 10.0)},
	704: {fmb6XX, signedShortScaled("bleTemp4", 10.0)},
	179: {anyModel, flag(model.PrefixOut + "1")},
	180: {anyModel, flag(model.PrefixOut + "2")},
	181: {anyModel, unsignedShortScaled(model.KeyPdop, 10.0)},
	182: {anyModel, unsignedShortScaled(model.KeyHdop, 10.0)},
	199: {anyModel, unsignedInt(model.KeyOdometerTrip)},
	200: {fmbXXX, unsignedByte("sleepMode")},
	205: {fmbOrTat, unsignedShort("cid2g")},
	206: {fmbOrTat, unsignedShort("lac")},
	236: {anyModel, alarmIf(model.AlarmGeneral)},
	239: {anyModel, flag(model.KeyIgnition)},
	240: {anyModel, flag(model.KeyMotion)},
	241: {anyModel, unsignedInt(model.KeyOperator)},
	246: {fmbXXX, alarmIf(model.AlarmTow)},
	247: {fmbXXX, alarmIf(model.AlarmAccident)},
	249: {fmbXXX, alarmIf(model.AlarmJamming)},
	251: {fmbXXX, alarmIf(model.AlarmIdle)},
	252: {fmbXXX, alarmIf(model.AlarmPowerCut)},
	253: {anyModel, func(p *model.Position, b *kit.Buf) {
		switch b.ReadUnsignedByte() {
		case 1:
			p.AddAlarm(model.AlarmAcceleration)
		case 2:
			p.AddAlarm(model.AlarmBraking)
		case 3:
			p.AddAlarm(model.AlarmCornering)
		}
	}},
	175: {fmbXXX, func(p *model.Position, b *kit.Buf) {
		if b.ReadUnsignedByte() > 0 {
			p.AddAlarm(model.AlarmGeofenceEnter)
		} else {
			p.AddAlarm(model.AlarmGeofenceExit)
		}
	}},
	636:   {fmbOrTat, unsignedInt("cid4g")},
	662:   {fmbXXX, flag(model.KeyDoor)},
	10644: {fmbXXX, signedShortScaled("tempProbe1", 100.0)},
	10645: {fmbXXX, signedShortScaled("tempProbe2", 100.0)},
	10646: {fmbXXX, signedShortScaled("tempProbe3", 100.0)},
	10647: {fmbXXX, signedShortScaled("tempProbe4", 100.0)},
	10648: {fmbXXX, signedShortScaled("tempProbe5", 100.0)},
	10649: {fmbXXX, signedShortScaled("tempProbe6", 100.0)},
	10800: {fmbXXX, signedShortScaled("eyeTemp1", 100.0)},
	10801: {fmbXXX, signedShortScaled("eyeTemp2", 100.0)},
	10802: {fmbXXX, signedShortScaled("eyeTemp3", 100.0)},
	10803: {fmbXXX, signedShortScaled("eyeTemp4", 100.0)},
	10832: {fmbXXX, signedShort("eyeRoll1")},
	10833: {fmbXXX, signedShort("eyeRoll2")},
	10834: {fmbXXX, signedShort("eyeRoll3")},
	10835: {fmbXXX, signedShort("eyeRoll4")},
}
