# Porting a protocol

Each Java protocol (`src/main/java/org/traccar/protocol/<Name>Protocol*.java`) becomes one Go
package, `internal/protocol/<name>` (the Java name lowercased, as `BaseProtocol.getName`
makes it). The port is checked against the Java tests' recorded calls (`oracle`) and, for
TCP/UDP protocols, against the Java server in the harness (`compat -suite protocols`).

The Go code follows the Java decoder line by line where that decides the output (field order,
which exceptions end a message, which values are Integer, Long or Double), and is plain Go
everywhere else: no classes, no getters, functions and small structs.

## Package shape

```go
// Package foo is the Foo protocol (FooProtocol): <one line on the transport and format>.
package foo

// Protocol is the protocol's definition.
func Protocol() ingest.Protocol            // or Protocol(cfg *config.Config) (ingest.Protocol, error)
                                           // when the decoder reads config keys at startup
func Framer() ingest.Framer                // FooFrameDecoder, if any
type Decoder struct{ ... }                 // FooProtocolDecoder; a func with ingest.DecoderFunc
func (d *Decoder) Decode(m *ingest.Message, msg any) any // if it has no state
func encode(m *ingest.Message, c *model.Command) any     // FooProtocolEncoder, if any
```

`ingest.Protocol{Name, Servers, DataCommands: protocol.DataCommands("foo")}`. One
`ingest.Server` per Java `addServer`: `Datagram: true` for UDP, `HTTP: decoder` for HTTP
protocols. `NewPipeline` builds one connection's handlers: `Framer`, `Text` (a
`StringDecoder` before the decoder: it gets a `string`), `TextOut` (a `StringEncoder`: string
replies and commands can be sent), `Decoder`, `Encoder`. A new pipeline is made per TCP
connection and once per UDP socket, as Netty does, so decoder state lives in the `Decoder`
struct exactly as in the Java object.

Register the protocol in `internal/protocol/all/all.go`.

## Java to Go

| Java | Go |
| --- | --- |
| `ByteBuf` reads, gets, writes, `indexOf`, `readSlice` | `kit.Buf`, same names (`ReadUnsignedByte`, `ReadInt` returns int32, `ReadUnsignedInt` int64, `GetShort`...), `ReadSignedByte` for `readByte`, `WriteByte8` for `writeByte` |
| `Unpooled.buffer()` / `wrappedBuffer(bytes)` | `kit.Empty()` / `kit.NewBuf(b)` |
| `ByteBufUtil.hexDump(buf)` / `DataConverter.parseHex` | `buf.HexDump()` / `kit.ParseHex`, `kit.Hex` (a Buf) |
| `buf.toString(US_ASCII)`, `readCharSequence(n, US_ASCII)` | `kit.Latin1(buf.Bytes())`, `buf.ReadString(n)`: **Netty decodes US_ASCII one char per byte**, unlike the JDK |
| `new String(bytes, US_ASCII)` / `s.getBytes(US_ASCII)` | `kit.ASCII` (U+FFFD above 0x7F) / `kit.ASCIIBytes` |
| `toString(UTF_8)`, StringDecoder | `kit.UTF8` |
| `Unpooled.copiedBuffer(s, US_ASCII)`, `writeCharSequence` | `kit.NettyBytes(s)`, `buf.WriteASCII(s)` |
| `Integer.parseInt(s[, radix])`, `Long.parseLong`, `Double.parseDouble` | `kit.Int`, `kit.IntRadix`, `kit.Long`, `kit.LongRadix`, `kit.Double` (they throw as Java does) |
| `String.split(regex)` | `kit.Split(s, literal)` (trailing empty strings dropped), `kit.SplitRegex`, `strings.Split` only for `split(x, -1)` |
| `length()`, `substring`, `charAt`, `indexOf`, `trim()` | `kit.Len`, `kit.Substring`/`kit.SubstringFrom`, `kit.CharAt`, `kit.IndexOf`/`kit.IndexOfFrom`, `kit.Trim` (UTF-16 indexes; never slice Go strings with Java indexes) |
| `String.format` | `kit.Format` (Java's rounding and two's-complement hex; pass `int` for Integer, `int64` for Long) |
| `PatternBuilder`, `Parser` | `kit.NewPattern()...Compile()`, `kit.NewParser`: `NextInt()` is `*int` (Java's nullable Integer); `MustInt()` unboxes (throws when the group is missing); `NextIntDefault(0)` is `nextInt(0)` |
| `DateBuilder` | `kit.NewDateBuilder(zone)` (nil zone is UTC) |
| `DateUtil.parse(DateTimeFormatter.ofPattern(p).withZone(z), s)` | `kit.NewDateFormat(p, z).Parse(s)` |
| `BitUtil`, `BcdUtil`, `BufferUtil`, `Checksum` | `kit.Check`, `kit.Between`, `kit.From`, `kit.To` (and `...Int` for int arguments), `kit.ReadBCD`, `kit.ReadSignedMagnitudeInt`, `kit.IndexOfCount`, `kit.CRC16`... |
| `UnitsConverter` | `units` |
| `new Position(getProtocolName())` | `m.NewPosition()` |
| `position.set(key, value)` | `pos.Set(key, value)`: like Java it skips null (nil, nil pointers) and `""`; `int` is Integer, `int64` Long, `float64` Double |
| `setLatitude`, `setLongitude` | `pos.SetLatitude`, `pos.SetLongitude` (they throw when out of range, as Java's) |
| `setTime(d)` / `setFixTime` / `setDeviceTime` | `pos.SetTime(t)` / `pos.FixTime = model.NewTime(t)` |
| `addAlarm(x)` (null ignored) | `pos.AddAlarm(x)` ("" ignored) |
| `Position.KEY_*`, `ALARM_*`, `PREFIX_*`, `Command.TYPE_*`, `Command.KEY_*` | `model.Key...`, `model.Alarm...`, `model.Prefix...`, `model.Command...`, `model.CommandKey...` |
| `new Network(cellTower)`, `network.addCellTower` | `model.NetworkOf(...)`, `network.AddCellTower` (HashSet order, as Java) |
| `CellTower.from(mcc, mnc, lac, cid[, rssi])`, `WifiAccessPoint.from` | `model.NewCellTower`, `model.NewCellTowerRSSI`, `model.NewWifiAccessPoint` |
| `getDeviceSession(channel, remote, ids...)` | `m.Session(ids...)` (nil: unknown device) |
| `deviceSession.get/set` | `ds.Get/Set` |
| `getLastLocation(position, date)` | `m.LastLocation(pos, &t)` (only when the device id is set, as Java) |
| `channel.writeAndFlush(new NetworkMessage(x, remote))` | `m.Send(x)` (`*kit.Buf` or, with `TextOut`, a string); `if (channel != null)` needs no check |
| `getConfig().getBoolean(Keys.X.withPrefix(getProtocolName()))` | read at construction: `cfg.Bool(name+".x", default)` |
| `AttributeUtil.lookup(cacheManager, key, deviceId)` | `m.LookupInt/LookupBool/LookupDouble/LookupString(deviceID, key, server, cfg)` |
| `getTimeZone(deviceId[, def])` | `m.TimeZone(deviceID, def)` |
| `getServer(channel, ':')` | `m.Server(':')` |
| `getMediaBuffer`, `writeMediaFile` | a `*kit.Buf` from `m.NewMediaBuffer()` in the decoder, `m.WriteMedia(id, ext, data)` |
| `new Date()` | `m.Now` (the receive time) |
| `getCommandsManager().readQueuedCommands` | `m.QueuedCommands(deviceID, limit)` |
| an exception | `kit.Throw(...)`, or just let a Go index panic happen where Java's array access throws |

Return what Java returns: `nil` for null, a `*model.Position`, or `[]*model.Position`. An empty
list is not null: `positions := []*model.Position{}` where Java makes `new LinkedList<>()`
(only null makes a `database.saveEmpty` position).

Encoders: `ingest.CommandEncoderFunc(encode)`; `formatCommand(command, format, keys...)` is
`kit.FormatCommand(c, m.UniqueID(c.DeviceID), format, keys...)`; `command.getString/
getInteger(...)` are `kit.CommandString/CommandInt`. Return a `*kit.Buf` for a ByteBuf, a
string for a String, nil for null.

HTTP protocols (`BaseHttpProtocolDecoder`): implement `ingest.HTTPDecoder`, as `osmand` does.

## Tests

Each package has `<name>_test.go` replaying the Java test classes:

```go
func TestJavaDecoder(t *testing.T) {
	oracle.Run(t, "FooProtocolDecoderTest", oracle.Handlers{
		Decoder: func(method string, env *oracle.Env) ingest.Decoder { return &Decoder{} },
	})
}
```

`Handlers.Before` sets what a test changes between calls (`decoder.setX(...)` at a line);
`Framer` and `Encoder` replay `FooFrameDecoderTest` and `FooProtocolEncoderTest`. The records
hold everything Java produced (every field, attribute types, the network, errors), so a test
passes only when the Go output is identical. The environment is Traccar's `BaseTest`: device
1 once a decoder passes unique ids, an empty config, the protocol name `unknown` (config keys
read with that prefix), and encoders see device unique id `123456789012345`.

Recording more Java calls: see `oracle/README.md`.
