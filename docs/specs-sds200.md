# Uniden SDS200 Network Protocol Specification

Scope: all network-facing protocols supported by the SDS200 over Ethernet, covering remote control, telemetry, audio streaming, and filesystem access. This document is SDS200-specific; model-specific differences are noted only where the protocol response format varies.

Primary sources: Uniden's official protocol PDFs from the [SDS200 Firmware Update page][1].

Terminology note: document revisions such as **Remote Command Spec V1.02/V2.00**
are protocol document versions, not scanner firmware numbers. SDS200 firmware
versions are reported by `VER` as radio main/sub firmware values.

---

## 1. Network Services Overview

The SDS200 exposes three network services over its Ethernet interface:

| Service | Transport | Port | Purpose |
|---------|-----------|------|---------|
| Remote control & telemetry | UDP | **50536** | ASCII/XML command protocol ("Virtual Serial on Network") |
| Audio streaming | RTSP/RTP | **554** (RTSP control, TCP) | G.711 u-law audio over RTP (UDP) |
| File access | FTP | Configurable | Read/write microSD filesystem |

---

## 2. IP Configuration (LAN Settings)

The scanner's LAN settings menu supports:

- LAN enable/disable
- IP assignment: DHCP ("Auto") or static (IP, subnet mask, gateway, DNS)
- FTP server enable/disable with separate read-only and read-write account credentials

---

## 3. UDP Remote Control Protocol

**Source:** [Virtual Serial on Network Specification V1.00][2] (2019-01-23)

### 3.1 Transport

- **Protocol:** UDP to port **50536** on the scanner
- Port chosen for backward compatibility with BCD536HP
- **No session negotiation. No protocol header.** Send a datagram, receive a response.

### 3.2 Command Framing

- Commands are plain ASCII terminated with **`\r`** (carriage return, 0x0D)
- Request format: `CMD[,arg1][,arg2]...\r`
- Response format: `CMD,<result...>\r`

### 3.3 Response Types

**A) Normal (single-packet) responses**

One UDP datagram containing the complete response.

```
PC → Scanner:    MDL\r
Scanner → PC:    MDL,SDS200\r
```

**B) XML responses**

XML responses are prefixed with `CMD,<XML>,` and may be single-packet or multi-packet.

**Single-packet XML responses:** When the full XML fits in one UDP datagram, the scanner sends the complete document **without a `<Footer>` element**. The XML body uses embedded `\r` characters as line separators within the payload (not just as a terminator).

```
PC → Scanner:    GSI\r

Scanner → PC:    GSI,<XML>,\r
                 <?xml version="1.0" encoding="utf-8"?>\r
                 <ScannerInfo Mode="Menu tree" V_Screen="menu_selection">\r
                   <MenuSummary name="Show IPs" index="4293304272" />\r
                   <ViewDescription>\r
                   </ViewDescription>\r
                 </ScannerInfo>\r
```

**Multi-packet XML responses:** Large responses (e.g., `GLT`) are split across multiple UDP packets. Each packet includes a `<Footer>` element for reassembly:

| Attribute | Meaning |
|-----------|---------|
| `No="n"` | Packet sequence number (1-based) |
| `EOT="0"` | More packets follow |
| `EOT="1"` | **End of transmission** (last packet) |

**Reassembly procedure:**
1. If no `<Footer>` tag is present and the XML has a closing root tag, treat as a complete single-packet response
2. Otherwise, collect packets, tracking `Footer No` values
3. Detect gaps in sequence numbers to identify lost packets
4. On the packet with `EOT="1"`, transmission is complete
5. If packets are lost, **retry the entire request**

```
PC → Scanner:    GLT,FL\r

Scanner → PC:    GLT,<XML>,
                 <?xml version="1.0" encoding="utf-8"?>
                 <GLT>
                  <FL Index="0" Name="Favorites List 1" Monitor="On" Q_Key="1" N_Tag="None"/>
                  ...
                  <Footer No="1" EOT="0"/>

Scanner → PC:    GLT,<XML>,
                 <?xml version="1.0" encoding="utf-8"?>
                 <GLT>
                  <FL Index="19" Name="FL 6" ... />
                  ...
                  <Footer No="2" EOT="0"/>

Scanner → PC:    GLT,<XML>,
                 <?xml version="1.0" encoding="utf-8"?>
                 <GLT>
                  <FL Index="44" Name="FL ABC" ... />
                  ...
                  <Footer No="3" EOT="1"/>       ← End of Transmission
```

### 3.4 Character Encoding

- XML responses use `encoding="utf-8"`
- Display character fields (Lx_CHAR in STS/GST) may use Uniden-specific font tables; see the [Font Data Specification][8] referenced in the Remote Command spec
- If `Lx_CHAR` or `Lx_MODE` is all `0x20` (space), it is converted to empty string
- Commas within `Lx_CHAR` values are escaped as `\t` (tab)

---

## 4. Remote Command Set

**Source:** [Remote Command Specification V1.02][6] (2023-12-22, 38 pages). Based on BCD536HP/BCD436HP Remote Command Specification V1.05. Version history: 0.01 (2018-04-13) initial; 1.01 (2023-12-11) added Waterfall; 1.02 (2023-12-22) added GST command.

A newer version exists: [SDS Series Remote Command Specification V2.00][7] (covers SDS100/SDS200/SDS150). V2.00 version history: 1.03 (2024-12-04) added POF/GCS commands; 2.00 (2025-07-07) added GW2 and KAL commands. **SDS200 supports all commands from V2.00.**

### 4.0 Radio Firmware Requirements (SDS200)

Use this table to map protocol features to the **minimum required radio
capability level**:

| Feature/Command Set | Requirement on Radio Firmware | How to Validate |
|---------------------|-------------------------------|-----------------|
| Core commands (`MDL`..`GWF`, commands 1-30) | Firmware implementing the Remote Command V1.02 behavior | `MDL` responds with SDS200 and command set 1-30 works |
| Added in spec rev 1.03: `POF`, `GCS` | Firmware implementing 1.03 additions (2024-12-04) | `POF` returns `POF,OK`; `GCS` returns `GCS,...` |
| Added in spec V2.00: `KAL`, `GW2` labeling | Firmware implementing 2.00 additions (2025-07-07) | `KAL` accepted with no response; `GWF` may respond as `GW2,...` |

Important:
- There is no separate "v2 radio firmware" naming scheme on SDS200; "V2.00"
  refers to the remote-command specification document.
- Always detect capabilities at runtime by probing commands (`POF`, `GCS`,
  `KAL`) and handling `NG` or legacy responses for backward compatibility.

### 4.1 Command Summary

| # | Command | Function | Program Mode Only |
|---|---------|----------|-------------------|
| 1 | `MDL` | Get model info | |
| 2 | `VER` | Get firmware version | |
| 3 | `KEY` | Simulate key press | |
| 4 | `QSH` | Quick search hold mode | |
| 5 | `STS` | Get current status (legacy) | |
| 6 | `JNT` | Jump to number tag | |
| 7 | `NXT` | Next | |
| 8 | `PRV` | Previous | |
| 9 | `FQK` | Get/set favorites list quick keys | |
| 10 | `SQK` | Get/set system quick keys | |
| 11 | `DQK` | Get/set department quick keys | |
| 12 | `PSI` | Push scanner information (periodic XML) | |
| 13 | `GSI` | Get scanner information (one-shot XML) | |
| 14 | `GLT` | Get list (XML, multi-packet) | |
| 15 | `HLD` | Hold | |
| 16 | `AVD` | Set avoid option | |
| 17 | `SVC` | Get/set service type settings | |
| 18 | `JPM` | Jump mode | |
| 19 | `DTM` | Get/set date and time | |
| 20 | `LCR` | Get/set location and range | |
| 21 | `AST` | Analyze start | |
| 22 | `APR` | Analyze pause/resume | |
| 23 | `URC` | User record control | |
| 24 | `MNU` | Menu mode command | |
| 25 | `MSI` | Menu status info | |
| 26 | `MSV` | Menu set value | |
| 27 | `MSB` | Menu structure back | |
| 28 | `GST` | Get scanner status (waterfall) | |
| 29 | `PWF` | Push waterfall FFT information | |
| 30 | `GWF` | Get waterfall FFT information | |
| -- | `GW2` | Get waterfall FFT information (binary, no separator) | |
| -- | `KAL` | Keep Alive (no response) | |
| 31 | `POF` | Power OFF | |
| 32 | `GCS` | Get Charge Status | |
| 33 | `VOL` | Get/Set Volume Level Settings | |
| 34 | `SQL` | Get/Set Squelch Level Settings | |

### 4.2 Identification Commands

**MDL** -- Get Model Info

```
Request:   MDL\r
Response:  MDL,[MODEL_NAME]\r
           MODEL_NAME: "SDS100", "SDS200", or "SDS150GBT"
```

**VER** -- Get Firmware Version

```
Request:   VER\r
Response:  VER,[VERSION]\r
           VERSION: "Version x.xx.xx" (includes "Version " prefix)
```

### 4.3 Key / UI Control

**KEY** -- Simulate Key Press

```
Request:   KEY,[KEY_CODE],[KEY_MODE]\r
Response:  KEY,OK\r
```

`KEY_MODE` values: `P` (press), `L` (long press), `H` (hold), `R` (release)

**Key codes:**

| Code | SDS200 Key | Note |
|------|-----------|------|
| `M` | MENU | Menu key |
| `F` | (Rotary knob) | Function key / Rotary knob |
| `L` | AVOID | Avoid key |
| `1`-`9`, `0` | 1-9, 0 | Digit keys |
| `.` | . NO | Dot key |
| `E` | E yes | Enter key |
| `>` | (Rotary) | Rotary right |
| `<` | (Rotary) | Rotary left |
| `^` | (Rotary) | Rotary knob push |
| `V` | VOL | Volume knob push |
| `Q` | SQ | Squelch knob push |
| `Y` | REPLAY | Replay key |
| `A` | SOFT 1 | Soft key 1 |
| `B` | SOFT 2 | Soft key 2 |
| `C` | SOFT 3 | Soft key 3 |
| `Z` | ZIP | Zip key |
| `T` | SREV | Service Type key |
| `R` | RANG | Range key |

### 4.4 Status & Telemetry

**STS** -- Get Current Status (legacy)

```
Request:   STS\r
Response:  STS,[DSP_FORM],[L1_CHAR],[L1_MODE],[L2_CHAR],[L2_MODE],
           [L3_CHAR],[L3_MODE],...,[L20_CHAR],[L20_MODE],
           [RSV],[RSV],[RSV],[RSV],[RSV],
           [RSV],[RSV],[RSV],[RSV],[RSV]\r
```

Field details:
- `DSP_FORM`: 5–20 binary digits (each `0` or `1`), where `0` = Small Font, `1` = Large Font. The number of active display lines is determined by the length of this field.
- `Lx_CHAR`: Fixed-length character string, 24 or 30 characters (padded with spaces). See [Font Data Specification][8] for non-ASCII codes. Commas within the value are escaped as `\t` (tab). If all `0x20` (space), treat as empty.
- `Lx_MODE`: Fixed-length display mode string, 24 or 30 characters (matching `Lx_CHAR` length). Each character indicates the display attribute of the corresponding `Lx_CHAR` character: `' '` (space) = NORMAL_CHAR, `'*'` = REVERSE_CHAR, `'_'` = Underline.
- The number of `Lx_CHAR`/`Lx_MODE` pairs present depends on `DSP_FORM` length (5–20 lines).

> **Note:** STS is compatible with older scanners. **PSI is better than STS.** See [Font Data Specification][8] for non-ASCII character codes.

**GST** -- Get Scanner Status (for Waterfall)

```
Request:   GST\r

V1.02 Response:
  GST,[DSP_FORM],[L1_CHAR],[L1_MODE],...,[L20_CHAR],[L20_MODE],
  [MUTE],[RSV],[RSV],
  [WF_MODE],[FREQ],[MOD],[MF_POS],
  [CF],[LOWER],[UPPER],[RSV],[FFT_SIZE]\r

V2.00 Response (adds LED and color fields):
  GST,[DSP_FORM],[L1_CHAR],[L1_MODE],...,[L20_CHAR],[L20_MODE],
  [MUTE],[LED1],[LED2],
  [WF_MODE],[FREQ],[MOD],[MF_POS],
  [CF],[LOWER],[UPPER],[COLOR_MODE],[FFT_SIZE]\r
```

`Lx_CHAR` / `Lx_MODE` format is the same as for STS (see above).

| Field | Values |
|-------|--------|
| `WF_MODE` | 0: Normal Mode, 1: Waterfall, 2: Menu/Direct Entry. **Note:** WF_MODE 0 (Normal Mode) means the waterfall data fields are not in an effective state for SDS Link. |
| `FREQ` | Mark frequency |
| `MOD` | Modulation |
| `MF_POS` | Marker position |
| `CF` | Center frequency |
| `LOWER` | Lower frequency |
| `UPPER` | Upper frequency |
| `FFT_SIZE` | 0: 25%, 1: 50%, 2: 75%, 3: 100% |
| `LED1` | Alert LED color (V2.00+): 0=OFF, 1=BLUE, 2=RED, 3=MAGENTA, 4=GREEN, 5=CYAN, 6=YELLOW, 7=WHITE |
| `LED2` | Battery Charge LED color (V2.00+, **SDS150 only**; always 0 on SDS200): same values as LED1 |
| `COLOR_MODE` | Waterfall color mode (V2.00+): 0=Color, 1=Black background, 2=White background |

**PSI** -- Push Scanner Information (periodic XML)

```
Request:   PSI[,interval_ms]\r
Response:  PSI,OK\r
```

The scanner acknowledges with `PSI,OK\r`, then outputs XML status information **periodically** at the configured interval (milliseconds). Subsequent push updates arrive as unsolicited `PSI,<XML>,...` packets using the same XML format as GSI.

**GSI** -- Get Scanner Information (one-shot XML)

```
Request:   GSI\r
Response:  GSI,<XML>,...\r   (XML format, may be multi-packet)
```

GSI returns a single snapshot of scanner state.

#### PSI/GSI XML Structure

The root element is `<ScannerInfo>` with attributes:
- `Mode` -- current scanner mode (Scan Mode, Scan Hold, Tone-Out, Custom Search, Custom Search Hold, Quick Search, Quick Search Hold, Service Scan, Service Scan Hold, Trunk Scan, Trunk Scan Hold, Close Call Only, Close Call, Menu tree)
- `V_Screen` -- current view screen (conventional_scan, trunk_scan, custom_with_scan, cchits_with_scan, custom_search, quick_search, close_call, cc_searching, tone_out, wx_alert, discovery_conventional, discovery_trunking, reverse_frequency, repeater_find, direct_entry, menu_selection, menu_input, analyze_system_status, analyze, waterfall, plain_text)

**Elements present in all modes:**

| Element | Key Attributes |
|---------|---------------|
| `Property` | F (Off/On), VOL (0-29), SQL (0-19), Sig (0-4), WiFi (Off/0-3/AP), Battery (0.0-3.3), Att (Off/On/G-Att), Rec (Off/On), KeyLock (Off/On), P25Status (None/Data/P25/DMR/CAP/CON/DT3/XPT/NX9/NX4/ND9/ND4/IDS/NXD), Mute (Unmute/Mute), A_Led (Off/Blue/Red/Magenta/Green/Cyan/Yellow/White), Dir (Up/Down), Rssi (0-) |
| `AGC` | A_AGC (Off/On), D_AGC (Off/On) |
| `DualWatch` | PRI (Off/DND/Priority), CC (Off/DND/Priority), WX (Off/Priority) |
| `DispFormat` | (display format info) |
| `ViewDescription` | (when viewing override area -- see below) |
| `ReplayDescription` | (when in REPLAY mode) |

**Mode-dependent elements** (present only in relevant scanner modes):

| Element | Key Attributes |
|---------|---------------|
| `MonitorList` | Name, Index, ListType (FullDb/FL/SWS), Q_Key (0-99/None), N_Tag (0-99/None), DB_Counter (0-65535) |
| `System` | Name, Index, Avoid (Off/T-Avoid/Avoid), SystemType (Conventional/Motorola/EDACS/LTR/P25 Trunk/P25 One Frequency/MotoTRBO Trunk/DMR One Frequency/NXDN Trunk/NXDN One Frequency), Q_Key, N_Tag, Hold (Off/On) |
| `Department` | Name, Index, Avoid, Q_Key, Hold |
| `Site` | Name, Index, Avoid, Q_Key, Hold, Mod (Auto/NFM/FM) |
| `ConvFrequency` | Name, Index, Avoid, Freq (xxxx.xxxxMHz), Mod (Auto/AM/NFM/FM/WFM/FMB), N_Tag, Hold, SvcType, SAS, SAL, P_Ch, RecSlot (1/2/None), LVL (-3 to 3), IFX, TGID, U_Id |
| `TGID` | Name, Index, Avoid, TGID, SetSlot (1/2/Any), RecSlot, N_Tag, Hold, SvcType, P_Ch, LVL |
| `SiteFrequency` | Freq, SAS, SAD, IFX |
| `SrchFrequency` | Avoid, Freq, Mod, Hold, SAD, RecSlot, TGID, U_Id, IFX |
| `CcHitsChannel` | Name, Index, Avoid, CH_No (0-9), Freq, Mod, Hold, SAD, LVL, IFX |
| `SearchRange` | Lower, Upper, Mod, Step |
| `SearchBanks` | Index (0-9), BankStatus (bitmap), Name, BankNo |
| `CC_Bands` | BandStatus (bitmap) |
| `ToneOutChannel` | Name, Index, CH_No (0-31), Freq, Mod, Hold, LVL, IFX, ToneA, ToneB |
| `WxChannel` | Name, Index, CH_No (1-7), Freq (FM), Mod, Hold, LVL, IFX |
| `WxMode` | Mode ("Monitor Weather"/"Weather Alert"), SAME |
| `ConventionalDiscovery` | Lower, Upper, Mod, Step, Freq, SAD, RecSlot, PastTime, HitCount, TGID, U_Id, IFX |
| `TrunkingDiscovery` | SystemName, SiteName, TGID, TgidName, SAD, RecSlot, PastTime, HitCount, U_Id |
| `SystemStatus` | SystemName, SiteName, Signal (0-100), Quality (0-100), Activity (0-100), SystemID (0-0x1FFFF), SystemSubID (0-99), SiteID (0-4095), WacnID (0-0xFFFFF), NAC (0-0xFFF), Color (0-15), RAN (0-63), Area (0-1), Att, Freqs (0-16), P25Status |
| `RfPowerPlot` | Frequency, Modulation, SampleRate (100/200/400/800ms), Att, B01-B34 (0-100) |
| `Analyze` | Msg1, Msg2, SystemName, SiteName, Att |
| `WaterfallBand` | Lower, Center, Upper, Mod, Step, Span, Limit (0/1) | Note: Step values: 5kHz/6.25kHz/7.5kHz/833kHz/10kHz/12.5kHz/15kHz/20kHz/25kHz/50kHz/100kHz; Span values: 360kHz/720kHz/1.44MHz/2.88MHz/5.76MHz/8.64MHz/17.28MHz |
| `WaterfallSettings` | MF (xxxx.xxxxMHz), Gain (Auto/0-15) |

**ViewDescription sub-elements:**
- `InfoArea1` -- Quick keys status (scan mode) or Banks status (search mode)
- `InfoArea2` -- Secondary info area
- `OverWrite` -- Error/scanning message on channel name area
- `PopupScreen` -- Temporary popup (like a dialog), may contain `Button` elements with KeyCode
- `PlainText` -- Plain text view lines

**ReplayDescription sub-elements:**
- `File` -- Index attribute
- `ReplayMode` -- Mode attribute (e.g., "USER_REC")

**Basic rules for PSI/GSI responses:**

- **MyId** corresponds to RadioReference Database IDs. Format: `xxId=xx` (e.g., CountyId=5, AgencyId=15)

| HPDB ID | Description | RRDB ID |
|---------|-------------|---------|
| CountyId | Conventional System (County) | ctid |
| AgencyId | Conventional System (Agency) | aid |
| TrunkId | Trunked System | sid |
| CGroupId | Conventional Department | scid |
| CFreqId | Conventional Frequency | fid |
| SiteId | Trunked Site | siteId |
| TGroupId | Trunked Department | tgCid |
| Tid | Trunked Channel | tgId |

- **Index** is used for hold/avoid operations. Assigned when data is downloaded to RAM. **Invalid if DB_Counter changes.**
- **Name** fields: ASCII 0x20-0x7E, max 64 characters
- **Search with Scan** entries do not have MyId.

#### PSI/GSI Element Presence by V_Screen Mode

Each V_Screen mode includes only specific mode-dependent elements. The `Property`, `AGC`, `DispFormat`, `ViewDescription`, and `ReplayDescription` elements are present in **all** modes.

**Scan modes** (`conventional_scan`, `trunk_scan`, `custom_with_scan`, `cchits_with_scan`):
- All include: `MonitorList`, `System`, `Department`, `DualWatch`, `SearchRange` (except trunk_scan, cchits_with_scan)
- `conventional_scan` adds: `ConvFrequency`
- `trunk_scan` adds: `Site`, `TGID`, `SiteFrequency`
- `custom_with_scan` adds: `SrchFrequency`
- `cchits_with_scan` adds: `CcHitsChannel`, `CC_Bands`

**Search modes** (`custom_search`, `quick_search`, `close_call`, `cc_searching`):
- `custom_search`: `SrchFrequency`, `DualWatch`, `SearchRange`, `SearchBanks`
- `quick_search`: `SrchFrequency`, `DualWatch`, `SearchRange`
- `close_call`: `SrchFrequency`, `DualWatch`, `CC_Bands`
- `cc_searching`: `ToneOutChannel`, `DualWatch`

**Signal modes:**
- `tone_out`: `WxChannel`, `WxMode`
- `wx_alert`: `SrchFrequency`

**Temporary modes:**
- `reverse_frequency`: `SrchFrequency`
- `repeater_find`: `SrchFrequency`, `DualWatch`

**Discovery modes:**
- `discovery_conventional`: `ConventionalDiscovery`, `DualWatch`
- `discovery_trunking`: `TrunkingDiscovery`

**Analyze/Waterfall modes:**
- `analyze`: `Analyze`
- `waterfall`: `WaterfallBand`, `WaterfallSettings`

### 4.5 Navigation Commands

**QSH** -- Quick Search Hold Mode

```
Request:   QSH,[FRQ]\r
Response:  QSH,OK\r
```

Invalid when scanner is in Menu Mode, Direct Entry, or Quick Save operation.

**JNT** -- Jump Number Tag

```
Request:   JNT,[FL_TAG],[SYS_TAG],[CHAN_TAG]\r
Response:  JNT,OK\r

FL_TAG:   Favorites List Number Tag (0-99)
SYS_TAG:  System Number Tag (0-99)
CHAN_TAG:  Channel Number Tag (0-999)
```

**NXT** -- Next

```
Request:   NXT,[tkw],[xxx1],[xxx2],[COUNT]\r
Response:  NXT,OK\r

COUNT: slide counts (1-8)
```

**PRV** -- Previous

```
Request:   PRV,[tkw],[xxx1],[xxx2],[COUNT]\r
Response:  PRV,OK\r

COUNT: slide counts (1-8)
```

> The `tkw`, `xxx1`, `xxx2` parameters are target-key-word and index values documented in the "tkd and 1st,2nd opt" sheet of the spec. These vary by scanner mode and target type (see Section 4.10).

**JPM** -- Jump Mode

```
Request:   JPM,[JUMP_MODE],[INDEX]\r
Response:  JPM,OK\r
```

| JUMP_MODE | INDEX |
|-----------|-------|
| SCN_MODE | Channel Index (0xFFFFFFFF = scan from top) |
| CTM_MODE | Reserve |
| QSH_MODE | Reserve |
| CC_MODE | Reserve |
| WX_MODE | NORMAL, A_ONLY, SAME_1-5, ALL_FIPS |
| FTO_MODE | Reserve |
| WF_MODE | Reserve |
| IREC_MODE | Reserve |
| UREC_MODE | Folder Name |
| TDIS_MODE | Session Name |
| CDIS_MODE | Session Name |

> If temporary clock is set and the target mode is discovery or WX alert, the scanner sends an NG response.

### 4.6 Quick Key Commands

**FQK** -- Get/Set Favorites List Quick Keys

```
Get:   FQK\r
       → FQK,[S0],[S1],...,[S99]\r

Set:   FQK,[S0],[S1],...,[S99]\r
       → FQK,OK\r
```

**SQK** -- Get/Set System Quick Keys

```
Get:   SQK,[FAV_QK]\r
       → SQK,[FAV_QK],[SYS_QK],[S0],[S1],...,[S99]\r

Set:   SQK,[FAV_QK],[S0],[S1],...,[S99]\r
       → SQK,OK\r
```

**DQK** -- Get/Set Department Quick Keys

```
Get:   DQK,[FAV_QK],[SYS_QK]\r
       → DQK,[FAV_QK],[SYS_QK],[S0],[S1],...,[S99]\r

Set:   DQK,[FAV_QK],[SYS_QK],[S0],[S1],...,[S99]\r
       → DQK,OK\r
```

Quick Key status values (S0-S99):
- `0` -- does not exist
- `1` -- exists and is **disabled**
- `2` -- exists and is **enabled**

If the controller sends `0` for a non-existent key, the scanner ignores it.

### 4.7 Hold & Avoid Commands

**HLD** -- Hold

Holds system, department, or channel. **Cannot hold favorites list or site frequency.**

```
Request:   HLD,[tkw],[xxx1],[xxx2]\r
Response:  HLD,OK\r
```

**AVD** -- Set Avoid Option

Avoids or un-avoids targets. **Cannot avoid favorites list or site frequency.**

```
Request:   AVD,[tkw],[xxx1],[xxx2],[STATUS]\r
Response:  AVD,OK\r

STATUS:
  1 = Permanent Avoid
  2 = Temporary Avoid
  3 = Stop Avoiding
```

> Use GSI or GLT commands to read current avoid status.

> In Repeater Find mode, sending HLD/NXT/PRV cancels the mode and returns to the previous mode (Custom Search/Quick Search/Close Call).

### 4.8 Service Type & Configuration Commands

**SVC** -- Get/Set Service Type Settings

```
Get:   SVC\r
       → SVC,[PST1],[PST2],...,[PST37],[CST1],...,[CST10]\r

Set:   SVC,[PST1],[PST2],...,[PST37],[CST1],...,[CST10]\r
       → SVC,OK\r
```

Values: `0` = Off (not scanned), `1` = On (scanned)

37 preset service types (PST1-PST37): Multi-Dispatch, Law Dispatch, Fire Dispatch, EMS Dispatch, (non), Multi-Tac, Law Tac, Fire-Tac, EMS-Tac, (non), Interop, Hospital, Ham, Public Works, Aircraft, Federal, Business, (non), (non), Railroad, Other, Multi-Talk, Law Talk, Fire-Talk, EMS-Talk, Transportation, (non), (non), Emergency Ops, Military, Media, Schools, Security, Utilities, (non), (non), Corrections

10 custom service types (CST1-CST10): Custom 1-8, Racing Officials, Racing Teams

**DTM** -- Get/Set Date and Time

```
Get:   DTM\r
       → DTM,[DayLightSaving],[YYYY],[MM],[DD],[hh],[mm],[ss],[RTC Status]\r

Set:   DTM,[DayLightSaving],[YYYY],[MM],[DD],[hh],[mm],[ss]\r
       → DTM,OK\r

RTC Status: 0 = RTC NG, 1 = RTC OK
```

Implementation note (observed on SDS200 firmware in the field): `DTM` **Get**
responses may return month/day/time parts without zero-padding. Example:
`DTM,1,2026,3,9,7,51,9,1`.

- Clients should parse `MM`,`DD`,`hh`,`mm`,`ss` as integer fields (accept both
  one-digit and two-digit values).
- For `DTM` Set requests, continue sending zero-padded values (`01`-`12`,
  `00`-`59`) for maximum compatibility.

**LCR** -- Get/Set Location and Range

```
Get:   LCR\r
       → LCR,[LATITUDE],[LONGITUDE],[RANGE]\r

Set:   LCR,[LATITUDE],[LONGITUDE],[RANGE]\r
       → LCR,OK\r
```

Latitude and longitude are in **degree format**.

### 4.9 List Commands (GLT)

**GLT** -- Get List

GLT returns database list contents as multi-packet XML.

**Request variants:**

| Request | Returns |
|---------|---------|
| `GLT,FL` | Favorites Lists |
| `GLT,SYS,[fl_index]` | Systems in favorites list |
| `GLT,DEPT,[system_index]` | Departments in system |
| `GLT,SITE,[system_index]` | Sites in system |
| `GLT,CFREQ,[dept_index]` | Conventional frequencies in department |
| `GLT,TGID,[dept_index]` | TGIDs in department |
| `GLT,SFREQ,[site_index]` | Site frequencies |
| `GLT,AFREQ` | Search avoiding frequencies |
| `GLT,ATGID,[system_index]` | Search avoiding TGIDs |
| `GLT,FTO` | Fire Tone Out channels |
| `GLT,CS_BANK` | Custom Search Banks |
| `GLT,UREC` | User Record folders |
| `GLT,IREC_FILE` | Inner Record files |
| `GLT,UREC_FILE,[folder_index]` | User Record files in folder |
| `GLT,TRN_DISCOV` | Trunk Discovery sessions |
| `GLT,CNV_DISCOV` | Conventional Discovery sessions |

**Response fields by list type:**

| # | Type | Fields |
|---|------|--------|
| 1 | FL | FL, Index, Name, Monitor, Q_Key, N_Tag |
| 2 | SYS | Index, Name, MyId, Avoid, Type, Q_Key, N_Tag |
| 3 | DEPT | Index, Name, MyId, Avoid, Q_Key |
| 4 | SITE | Index, Name, Avoid, Q_Key |
| 5 | CFREQ | Index, MyId, Name, Avoid, Freq, Mod, SAS, SAL, SvcType, N_Tag |
| 6 | TGID | Index, Name, Avoid, TGID, Audio Type, SvcType, N_Tag |
| 7 | SFREQ | Index, Freq |
| 8 | AFREQ | Freq, Avoid |
| 9 | ATGID | TGID, Avoid, Index, Name, DeptName, DeptIndex |
| 10 | FTO | Index, Freq, Mod, Name, ToneA, ToneB |
| 11 | CS_BANK | Index, Name, Lower, Upper, Mod, Step |
| 12 | UREC | Index, Name |
| 13 | IREC_FILE | Index, Name, Time |
| 14 | UREC_FILE | Index, Name, Time |
| 15 | TRN_DISCOV | Name, Delay, Logging, Duration, CompareDB, SystemName, SystemType, SiteName, TimeOutTimer, AutoStore |
| 16 | CNV_DISCOV | Name, Lower, Upper, Mod, Step, Delay, Logging, CompareDB, Duration, TimeOutTimer, AutoStore |

**Example GLT,FL response:**

```
GLT,FL\r
GLT,<XML>,\r
<?xml version="1.0" encoding="utf-8"?>\r
<GLT>\r
  <FL Index="0" Name="Favorites List 1" Monitor="On" Q_Key="1" N_Tag="None"/>\r
  <FL Index="1" Name="Favorites List 2" Monitor="On" Q_Key="2" N_Tag="2"/>\r
  <FL Index="2" Name="Favorites List 3" Monitor="Off" Q_Key="5" N_Tag="999"/>\r
</GLT>\r
```

### 4.10 Target Key Word (tkw) Parameter Reference

The `tkw`, `xxx1`, and `xxx2` parameters used by NXT, PRV, HLD, and AVD commands vary by target type and scanner mode:

| Target | GLT key | NXT/PRV 1st | NXT/PRV 2nd | HLD 1st | HLD 2nd | AVD 1st | AVD 2nd |
|--------|---------|-------------|-------------|---------|---------|---------|---------|
| Favorites List | FL | -- | -- | -- | -- | -- | -- |
| System | SYS | Sys Index | (none) | Sys Index | (none) | Sys Index | (none) |
| Department | DEPT | Dept Index | Parent Sys Index | Dept Index | Parent Sys Index | Dept Index | (none) |
| Site | SITE | Site Index | (none) | Site Index | (none) | Site Index | (none) |
| Conv. Frequency | CFREQ | Chan Index | (none) | Chan Index | (none) | Chan Index | (none) |
| TGID (scan) | TGID | TGID | (Site Index) | TGID | (Site Index) | -- (Use ATGID) | Parent sys index |
| Site TGID | STGID | -- | -- | -- | -- | -- | -- |
| Site Frequency | SFREQ | -- | -- | -- | -- | -- | -- |
| Avoid TGID | ATGID | -- | -- | -- | -- | TGID | Parent sys index |
| Search Avoiding Freq | AFREQ | -- | -- | -- | -- | Frequency | (none) |
| CC Hits Channel | CCHIT | CC Chan Index | -- | CC Chan Index | (none) | CC Chan Index | (none) |
| Close Call | CC | (none) | (none) | (none) | (none) | -- (Use AFREQ) | -- |
| WX | WX | WX Chan Index | (none) | WX Chan Index | (none) | -- | -- |
| Tone Out | FTO | FTO Chan Index | (none) | FTO Chan Index | (none) | -- | -- |
| Custom Search Bank | CS_BANK | -- | -- | -- | -- | -- | -- |
| Custom Search Freq | CS_FREQ | Frequency | Parent Bank Index | Frequency | (none) | -- (Use AFREQ) | -- |
| Search w/ Scan Freq | SWS_FREQ | Frequency | Parent Dept Index | Frequency | Parent Dept Index | -- (Use AFREQ) | -- |
| Quick Search Freq | QS_FREQ | Frequency | (none) | Frequency | (none) | -- (Use AFREQ) | -- |
| Repeater Find Freq | RPTR_FREQ | Frequency | (none) | Frequency | (none) | -- (can't avoid) | -- |
| Inner Record File | IREC_FILE | -- | -- | File Index | (none) | -- (can't avoid) | -- |
| User Record Folder | UREC | -- | -- | -- (can't select) | -- | -- (can't avoid) | -- |
| User Record File | UREC_FILE | -- | -- | File Index | (none) | -- (can't avoid) | -- |
| Trunk Discovery | TRN_DISCOV | -- | -- | -- | -- | TGID | (none) |
| Conv. Discovery | CNV_DISCOV | -- | -- | -- | -- | Frequency | -- |
| Band Scope | BAND_SCOPE | Frequency | (none) | Frequency | (none) | -- | -- |

> **Note on avoid syntax:** To avoid a frequency in Quick Search mode, use `AVD,AFREQ,4060000,,1\r` (not `AVD,QS_FREQ,...`). Avoid commands always use the `AFREQ` or `ATGID` target keywords.

> **Note on "Unknown" department:** In ID Search mode, the "Unknown" department is a virtual department. It can be held, next'd, and previous'd, but **cannot be avoided**. It requires the parent system index as the 2nd parameter. Other departments accept either blank or system index for the 2nd parameter.

### 4.11 Analyze / Waterfall Commands

**AST** -- Analyze Start

Sub-commands:

**Current Activity:**
```
Request:   AST,CURRENT_ACTIVITY,[Site Index]\r
Response:  XML at 200ms intervals
```

XML contains `<CurrentActivity>` elements. Control channel entries have: LCN, Freq, SystemID (hex), SiteID, TgidType="Control Channel". Voice channel entries have: LCN, Freq, TGID, UnitID, MOD (Analog/Digital/Encrypted), TgidType (Encrypted/Patch/Unknown/TGID/I-CALL).

> **Protocol typo:** The scanner's XML uses `Tgidype` (missing 'T') instead of `TgidType` on some entries. Implementations should handle both forms.

**LCN Monitor:**
```
Request:   AST,LCN_MONITOR,[Site Index]\r
Response:  XML at 1s intervals
```

XML contains `<LcnMonitor>` elements with: LCN, Freq, ReceiveStatus (0/1).

> **Protocol typo:** The scanner's XML uses `ReceiveStaus` (misspelled) as the attribute name, not `ReceiveStatus`. Implementations must match the misspelled form.

**Activity Log:**
```
Request:   AST,ACTIVITY_LOG,[Site Index]\r
Response:  AST,ACTIVITY_LOG,[Time],[Data],[Message],[Description1-5]
```

Time format: `MM/DD/YYYY hh:mm:ss`. If a temporary clock was set, the scanner returns an NG response.

Data/Message/Description content depends on system type:

**Motorola Activity Log:**
- Data format: `<cmd>/<prv>/<id>` (cmd: 0-1023, prv: 0/1, id: 0-65535)
- Messages: System ID, Site ID, Talkgroup Voice Channel Grant, Talkgroup Voice Channel Grant Update, I-Call Voice Channel Grant Update, Individual Call, Patch/MultiSelect Voice Channel Grant, Patch/Multiselect Voice Channel Grant Update, Patch List, Patch Cancel, Control, First OSW, Receive Error
- Description fields: Sid (hex), Site (decimal), Tid (decimal), Uid (decimal), Pid (decimal), Mid (decimal), Lcn (decimal), Sts (status bit), Mod (Analog/Digital)

**P25 Standard Activity Log:**
- Data format: `<opcode>/<data>` (opcode: 1 byte hex, data: 12 bytes hex for TSBK)
- Messages include: Group Voice Channel Grant (explicit/update), Unit To Unit Voice Channel Grant (extended), Status Update/Query, Message Update, Radio Unit Monitor Command, Call Alert, Deny Response, Group Affiliation Response/Query, Unit Registration Response/Command, Authentication Command, Time and Date Announcement, System/Network/RFSS/Adjacent Status Broadcasts, Identifier Updates, and many more
- Description fields: Lcn, LcnT (transmit), LcnR (receive), Gad (group address), Sad (source address), Tad (target address), Src (source ID), Iden (identifier), Bw (bandwidth), Tofs (transmit offset), Csp (channel spacing), Bfrq (base frequency), Sid (system ID hex), Sub (RF subsystem ID), Site, Wacn (hex), Type (channel type)

**EDACS Activity Log:**
- Data format: `<data>` (28 bits hex)
- Messages: Site ID, Talkgroup Voice Channel Grant/Update, I-Call Voice Channel Grant Update, Patch Voice Channel Grant/Update, Patch List, First OSW, Receive Error
- Description fields: Site, Tid (decimal; AFS for 1-2047, decimal for 2048-65535), Uid, Pid, Mid, Lcn, Sts

**LTR Activity Log:**
- Data format: `<area_code>/<goto>/<home>/<id>/<free>`
- Messages: Repeater Idle, Talkgroup Voice Channel Grant Update, Turn-off Code
- Description fields: Tid (Area-Home-Id format), Rpt (transmitting repeater), Goto, Free

**DMR/MotoTRBO Activity Log:**
- Data format: `<opcode>/<fid>/<id>/<ch>/<slot>/<prv>/<emergency>` (opcode: hex, fid: 00=DMR/06=Connect Plus/10=Capacity Plus, slot: 1/2/15=None)
- Messages: Talkgroup/Unit-to-Unit Voice Channel Grant and Link Control, Broadcast Talkgroup Voice Channel Grant, Capacity Plus Voice Channel Grant/Update/Site ID, Linked Capacity Plus Site ID, Connect Plus Voice Channel Grant/Update/Network ID, DMR Network ID, Idle
- Description fields: Sid (network ID hex), Site, Tid, Uid, Uid Src, Uid Dst, Color Code, Lcn, Slot

**NXDN Activity Log:**
- Data format: `<call type>/<home ch>/<id>/<ch>/<prv>/<emergency>` (call type: 0-7, home ch: 0-31 IDAS only)
- Messages: Replying to requesting communication, Performing voice communication, Sending Encryption init vector, Assignment of traffic channel to VC, Transmission released, Idle, Disconnecting, Site configuration information, Service information, Control channel information, IDAS go to Repeater
- Description fields: Sys (system ID), Site, Tid, Uid, Uid Src, Uid Dst, RAN, Area Code, LCN, Go to Repeater, Home Ch, Cch LCN, DFA (direct frequency assignment)

**LCN Finder:**
```
Request:   AST,LCN_FINDER,[Site Index]\r
Response:  XML at 500ms intervals
```

XML contains `<LcnFinder>` elements with Freq, AccuracyStatus (30-char string, one per LCN: 0=Unknown, 1-4=Level, 5=Found, 6=Disable), and `<LcnFinder Condition="Searching"/"All Lcn Found"/>`.

**System Status:**
```
Request:   AST,SYSTEM_STATUS,[site_index]\r
Response:  AST,OK\r
```

**Band Scope** (SDS200 only, not available on SDS100):
```
Request:   AST,BAND_SCOPE,[Center frequency],[Span],[Step],[Modulation]\r
Response:  AST,BAND_SCOPE,[Frequency],[RSSI_LEVEL]\r  (output at 10ms intervals on every frequency change)
```

| Parameter | Values |
|-----------|--------|
| Center frequency | 250000-1300000 (Hz format) |
| Span | 200-8000 |
| Step | 500, 625, 750, 833, 1000, 1500, 2000, 2500, 5000, 10000 |
| Modulation | Auto, AM, NFM, FM, WFM, FMB |
| RSSI_LEVEL | 0-100 |

**RF Power Plot** (SDS200 only, not available on SDS100):
```
Request:   AST,RF_POWER_PLOT,[Frequency],[Modulation],[Sampling Rate]\r
Response:  AST,OK\r
```

| Parameter | Values |
|-----------|--------|
| Frequency | 250000-1300000 (Hz format) |
| Modulation | Auto, AM, NFM, FM, WFM, FMB |
| Sampling Rate | 100, 200, 400, 800 (ms) |

**Raw Data Output** (USB interface only, not available over network):
```
Request:   AST,RAW_DATA_OUTPUT,[Frequency],[Modulation],[Filter],[Global Attenuator]\r
```

| Parameter | Values |
|-----------|--------|
| Frequency | 250000-13000000 (Hz format) |
| Modulation | Auto, AM, NFM, FM, WFM, FMB |
| Filter | 1=On, 0=Off |
| Global Attenuator | 1=On, 0=Off |

Outputs 10-bit signed discriminator A/D sampling data, split into high and low bytes:
- **High byte:** b7=1, b6=0, b5=0, b4=bit9, b3=bit8, b2=bit7, b1=bit6, b0=bit5
- **Low byte:** b7=0, b6=0, b5=0, b4=bit4, b3=bit3, b2=bit2, b1=bit1, b0=bit0

> Before sending AST commands, go to Scan Mode to load the HPDB data.

**APR** -- Analyze Pause/Resume

```
Request:   APR,[Analyze Mode]\r
Response:  APR,OK\r

Analyze Mode: SYSTEM_STATUS, RF_POWER_PLOT, CURRENT_ACTIVITY,
              LCN_MONITOR, ACTIVITY_LOG, RAW_DATA_OUTPUT
```

**PWF** -- Push Waterfall FFT Information

```
Request:   PWF,[FFT_TYPE],[ON/OFF]\r
Response:  PWF,[DATA1],[DATA2],...,[DATA_n]\r  (continuous push)

FFT_TYPE: 1 = Displayed FFT (240 data points)
```

**GWF** -- Get Waterfall FFT Information

```
Request:   GWF,[TYPE],[ON/OFF]\r
Response:  GWF,[DATA1],[DATA2],...,[DATA240]\r

TYPE: 1 = Displayed FFT (240 data points)
```

### 4.12 Recording Control

**URC** -- User Record Control

```
Get status:  URC\r
             → URC,[STATUS]\r

Set:         URC,[STATUS]\r
             → URC,OK\r
             → URC,ERR,[ERROR CODE]\r  (on failure)

STATUS: 0 = Stop, 1 = Start

ERROR CODES:
  0001: FILE ACCESS ERROR
  0002: LOW BATTERY
  0003: SESSION OVER LIMIT
  0004: RTC LOST
```

### 4.13 Menu Mode Commands

These commands provide remote access to the scanner's menu system. They depend on menu structure and firmware behavior, making them more fragile than other commands.

**MNU** -- Enter Menu Mode

```
Request:   MNU,[MENU_ID],[INDEX]\r
Response:  MNU,OK\r
```

| MENU_ID | INDEX | Menu Position |
|---------|-------|---------------|
| TOP | - | Top (Main) Menu |
| MONITOR_LIST | - | Select Lists to Monitor |
| SCAN_SYSTEM | System Index | System Menu |
| SCAN_DEPARTMENT | Department Index | Department Menu |
| SCAN_SITE | Site Index | Site Menu |
| SCAN_CHANNEL | Channel Index | Channel Menu |
| SRCH_RANGE | Custom Bank Index | Custom Search Bank Menu |
| SRCH_OPT | - | Search/Close Call Opt Menu |
| CC | - | Close Call Menu |
| CC_BAND | - | Close Call Band Menu |
| WX | - | WX Operation Menu |
| FTO_CHANNEL | FTO Channel Index | Tone Out Channel Menu |
| SETTINGS | - | Settings Menu |
| BRDCST_SCREEN | - | Broadcast Screen Menu |

**MSI** -- Menu Status Info

```
Request:   MSI\r
Response:  MSI,<XML>,\r
           <?xml version="1.0" encoding="utf-8"?>\r
           <MSI Name="Title" Index="xxxxxx">\r
           ...
           </MSI>\r
```

MSI element attributes: Name (menu title), Index, MenuType (TypeSelect/TypeInput/TypeLocation/TypeError), Value (current), Selected.

Sub-elements by MenuType:
- **MenuItem**: Name, Index, Value
- **MenuInput**: MaxLength (1-64), EnableKeys, AddedInformation
- **MenuLocation**: MaxLength, EnableKeys, IsLatitude ("1"=Lat/"0"=Lon)
- **MenuErrorMsg**: Text (error message), ScanButton ("1"=Enable/"0"=Disable)

**MSV** -- Menu Set Value

```
Request:   MSV,[RSV],[VALUE]\r
Response:  MSV,OK\r
```

For select-type menus: VALUE = selected item index. For input-type menus: VALUE = inputted string.

> If VALUE contains a comma, replace `,` with `\t` (tab).

**MSB** -- Menu Structure Back

```
Request:   MSB,[RSV],[RET_LEVEL]\r
Response:  MSB,OK\r

RET_LEVEL:
  "RETURN_PREVOUS_MODE" = exit menu mode entirely  ← NOTE: literal spelling, "PREVOUS" not "PREVIOUS"
  "" (empty)            = go back 1 level
```

### 4.14 Commands Added in V2.00

These commands are present in the [SDS Series Remote Command Specification V2.00][7] but not in V1.02. They apply to SDS200 radios whose firmware includes the V2.00-era additions (see 4.0).

**GW2** -- Get Waterfall FFT Information (Binary, no separator)

Like GWF but returns FFT data as 240 binary values (no comma separators).
GW2 is not a separate wire command — it uses the same `GWF` request. Newer
firmware may label the response as `GW2` instead of `GWF`; implementations
should accept both.

```
Request:   GWF,[TYPE],[ON/OFF]\r
Response:  GWF,[FFT_DATA]\r  (or GW2,[FFT_DATA]\r on newer firmware)

TYPE: 1 = Displayed FFT (240 data points, binary)
```

**KAL** -- Keep Alive

```
Request:   KAL\r
Response:  (none — the scanner does NOT return any response for KAL)
```

> KAL is a fire-and-forget command. Do not wait for a response.

**POF** -- Power OFF

```
Request:   POF\r
Response:  POF,OK\r
```

**GCS** -- Get Charge Status

```
Request:   GCS\r
Response:  GCS,CST=[DATA1],VOLT=[DATA2]mV:[DATA3]%,CURR=[DATA4]mA,TEMP=[DATA5]C\n
```

| Field | Values |
|-------|--------|
| `DATA1` (CST) | 0=CHG_SUBSTS_NO_CHG (No Ext. Power or No Battery), 1=CHG_SUBSTS_BQ274_INIT (Initializing Gauge IC BQ27426), 2=CHG_SUBSTS_TEMP_NG (Abnormal Temperature), 3=CHG_SUBSTS_PWR_NG (Abnormal Power), 4=CHG_SUBSTS_FULL_CHG (Full Charge), 5=CHG_SUBSTS_RECHG (Recharge), 6=CHG_SUBSTS_CHARGE (Charging) |
| `DATA2` | Battery voltage in mV |
| `DATA3` | Remaining battery capacity (%) |
| `DATA4` | Charge(+) or Discharge(-) current in mA |
| `DATA5` | Battery temperature in Celsius |

Example: `GCS,CST=4,VOLT=4184mV:100%,CURR=0000mA,TEMP= 27.65C`

> Note: GCS is primarily relevant for battery-powered devices (SDS100, SDS150). On the SDS200 (AC-powered), behavior may differ.

**VOL** -- Get/Set Volume Level Settings

```
Get:   VOL\r
       → VOL,[LEVEL]\r

Set:   VOL,[LEVEL]\r
       → VOL\r

LEVEL: 0-29 (SDS200)
```

**SQL** -- Get/Set Squelch Level Settings

```
Get:   SQL\r
       → SQL,[LEVEL]\r

Set:   SQL,[LEVEL]\r
       → SQL\r

LEVEL: 0-19 (SDS200)
```

---

## 5. RTSP / RTP Audio Streaming

**Source:** [RTSP.pdf][3]

### 5.1 Summary

| Property | Value |
|----------|-------|
| RTSP version | RTSP/1.0 |
| Streaming media | Audio only (no video) |
| Media format | **G.711 u-law** (PCMU, RTP payload type 0) |
| Media filename | scanner.au |
| Media server name | Scanner Audio Server 0.0.1 |
| Cache-Control | no-cache |
| Transport | RTP/AVP, unicast |
| RTSP control port | **554** (TCP) |
| RTP audio | UDP (ports negotiated via SETUP) |

### 5.2 Supported RTSP Methods

`OPTIONS`, `DESCRIBE`, `SETUP`, `PLAY`, `GET_PARAMETER`, `TEARDOWN`

The `GET_PARAMETER` response includes:
```
Supported: play.basic, con.persistent
Public: DESCRIBE, SETUP, TEARDOWN, PLAY, OPTIONS, GET_PARAMETER
```

### 5.3 Session Flow

```
App                                          Scanner
 │                                              │
 │── OPTIONS rtsp://<ip>/au:scanner.au ────────>│
 │<──── RTSP/1.0 200 OK ──────────────────────│
 │                                              │
 │── DESCRIBE rtsp://<ip>/au:scanner.au ──────>│  (Accept: application/sdp)
 │<──── RTSP/1.0 200 OK ──────────────────────│  (Content-Type: application/sdp)
 │      with SDP body                           │
 │                                              │
 │── SETUP rtsp://<ip>/au:scanner.au/trackID=1 >│  (Transport: RTP/AVP;unicast;client_port=XXXX)
 │<──── RTSP/1.0 200 OK ──────────────────────│  (Transport: ...;source=<ip>;server_port=YYYY;ssrc=ZZZZ)
 │      Session: <id>;timeout=60                │
 │                                              │
 │── PLAY rtsp://<ip>/au:scanner.au/ ─────────>│  (Range: npt=0.000-)
 │<──── RTSP/1.0 200 OK ──────────────────────│  (RTP-Info: url=...;seq=1;rtptime=0)
 │                                              │
 │<════════════ RTP (UDP) audio stream ════════│
 │                                              │
 │── GET_PARAMETER (keepalive) ───────────────>│  (before 60s timeout)
 │<──── RTSP/1.0 200 OK ──────────────────────│
 │                                              │
 │── TEARDOWN rtsp://<ip>/au:scanner.au/ ─────>│  (no documented response body)
 │                                              │
```

### 5.4 SDP (Session Description)

Returned by DESCRIBE:

```
v=0
o=- 0000000000 IN IP4 127.0.0.1
s=scanner.au
c=IN IP4 0.0.0.0
t=0 0
a=sdplang:en
a=control:*
m=audio 0 RTP/AVP 0
a=control:trackID=1
```

Key points:
- `m=audio 0 RTP/AVP 0` -- media is audio, RTP/AVP profile, payload type **0** (PCMU / G.711 u-law, 8000 Hz, mono)
- `a=control:trackID=1` -- used in SETUP URL
- The DESCRIBE response already includes a `Session` header with the session ID (before SETUP)

### 5.5 SETUP / PLAY Response Details

**SETUP response Transport header fields:**

| Field | Description |
|-------|-------------|
| `client_port` | Echoed back from client request |
| `source` | Scanner's IP address |
| `server_port` | Scanner-assigned RTP port |
| `ssrc` | Scanner-assigned SSRC identifier |

**PLAY response headers:**

| Header | Example Value | Description |
|--------|--------------|-------------|
| `Range` | `npt=0.0-596.48` | Server-reported time range |
| `RTP-Info` | `url=rtsp://<ip>/au:scanner.au/trackID=1;seq=1;rtptime=0` | Initial RTP sequence and timestamp |

### 5.6 Session Management

- Session ID and `timeout=60` are returned in the SETUP response
- All subsequent requests (PLAY, GET_PARAMETER, TEARDOWN) must include the `Session` header
- **Send `GET_PARAMETER` before the 60-second timeout to keep the session alive**
- The scanner supports `play.basic` and `con.persistent` features

### 5.7 Practical Constraints

- **Single client only.** Only one application can access the audio stream at a time.
- After disconnecting, reconnection may fail. Power-cycling the scanner may be required.
- VLC can play the stream using `rtsp://<ip>/au:scanner.au` but introduces noticeable latency.

---

## 6. FTP / File Access & microSD Data Model

**Source:** [SDSx00 File Specification V1.08][4] (2025-10-16)

### 6.1 FTP Server

The scanner's FTP server provides read/write access to the microSD filesystem. It is configured through the scanner's LAN settings menu with separate read-only and read-write account credentials.

### 6.2 microSD Directory Structure

```
\ (root)
├── BCDx36HP\
│   ├── scanner.inf          Scanner info (ESN, model, firmware versions)
│   ├── profile.cfg          Profile and global settings
│   ├── discvery.cfg         Discovery session configuration
│   └── app_data.cfg         Scanner resume state (delete when writing program data)
│
├── audio\
│   ├── inner_rec\           Internal recordings
│   │   └── YYYY-MM-DD_hh-mm-ss.wav
│   ├── user_rec\            User recordings
│   │   └── XXXXXXXX\        Hex timestamp folder name (FAT time format)
│   │       └── YYYY-MM-DD_hh-mm-ss.wav
│   └── alert\               Alert recordings
│       └── YYYY-MM-DD_hh-mm-ss.wav
│
├── favorites_lists\
│   ├── f_list.cfg           Favorites list configuration (F-List entries)
│   └── f_XXXXXX.hpd         Favorites list data (XXXXXX = 000001-999999)
│
├── firmware\                Firmware files (downloaded from Sentinel)
│
├── HPDB\                    HomePatrol Database
│   ├── hpdb.cfg             State, county list, Locate Me info
│   ├── s_XXXXXX.avd         Avoid list (per state)
│   └── s_XXXXXX.hpd         Scan channel (per state)
│
├── discovery\
│   ├── Conventional\
│   │   └── Session0001_Run0001\           (Session name format)
│   │       ├── summary.log
│   │       ├── detail.log
│   │       └── <freq>_<mod>_<subaudio>\   Channel folders (Frequency_Modulation_SubAudio)
│   │           │   e.g. 25000000_AM_None, 225000000_NFM_D023, 851012500_NFM_NFF
│   │           │   SubAudio encodes: CTCSS/DCS/NAC/ColorCode/RAN/Area
│   │           └── ss-mm-hh_DD-MM-YYYY.wav
│   └── Trunk\
│       └── Session0001_Run0001\
│           ├── summary.log
│           ├── detail.log
│           └── <tgid>_<status>\           e.g. 125_A, 65535_A
│               └── ss-mm-hh_DD-MM-YYYY.wav
│
└── activity_log\
    └── YYYY-MM-DD_hh-mm-ss_SystemName.log
```

> File/folder names start with the recording start timestamp. Special characters (`\`, `/`, `:`, `*`, `?`, `"`, `<`, `>`, `|`) in system names are replaced with `_`. Filenames exceeding 64 characters are truncated.

### 6.3 Hex Timestamp Format (user_rec folder names)

User recording folders use a hexadecimal FAT time format:

```
Bit:  31───25  24──21  20──16  15──11  10───5  4───0
      YYYY-1980  MM     DD      hh      mm     ss÷2
```

Decoding:
```
year   = ((fat_time >> 25) & 0x7F) + 1980
month  = (fat_time >> 21) & 0x0F
day    = (fat_time >> 16) & 0x1F
hour   = (fat_time >> 11) & 0x1F
minute = (fat_time >> 5)  & 0x3F
second = (fat_time << 1)  & 0x3F
```

Example: `4D786CA` → 2018-11-24_13-37-24

### 6.4 File Format Rules

- **Encoding:** ASCII
- **Structure:** one line = one sentence; one sentence = one command + parameters
- **Delimiter:** Tab (`\t`) separates command from parameters; `=`, `/`, `,` divide sub-parameters
- **All files contain:** TargetModel (`BCDx36HP`), FileType, FormatVersion
- **Name tags:** ASCII 0x20-0x7E, max 64 characters
- **Frequency/Step values:** integer format where 1 = 1 Hz (e.g., 851.0125 MHz = `851012500`)
- **Latitude/Longitude:** degree format, up to 6 decimal places
- **Timestamps:** `MM/DD/YYYY hh:mm:ss`

### 6.5 scanner.inf

```
Scanner	Model	ESN	F_Ver	R_Ver	Reserve	Zip_Ver	City_Ver	WiFi_Ver	SCPU_Ver
```

| Field | Description |
|-------|-------------|
| Model | Model name |
| ESN | Electronic Serial Number |
| F_Ver | Firmware Version |
| R_Ver | Registry Version |
| Zip_Ver | Zip table version |
| City_Ver | City table version |
| WiFi_Ver | WiFi Module version |
| SCPU_Ver | SUB CPU version |

### 6.6 profile.cfg

Contains all global scanner settings. Key sections (tab-delimited, one per line):

**GlobalSetting:**

```
GlobalSetting	ScanHpdb	G-Attenuator	Reserve	Priority_Scan_mode	Close_Call_mode	WX_Priority_mode
	Priority_Interval	Priority_Max_Channels	Srch_Key_1	Srch_Key_2	Srch_Key_3
	Key_Lock	Key_Beep	Volume	Squelch	SearchWithScanList	SearchWithScan	System_Avoid
	Site_NAC_Operation	Global_Auto_Filter	Audio_Off_Time	Headphone_LR_output
```

| Field | SDS200 Values |
|-------|---------------|
| ScanHpdb | Off/On |
| G-Attenuator | On/Off |
| Priority Scan mode | Off/DND/Priority |
| Close Call mode | Off/DND/Priority |
| WX Priority mode | Off/Priority |
| Priority Interval | 1-10 (sec) |
| Priority Max Channels | 1-100 |
| Srch Key 1/2/3 | Off, Custom0-Custom9, ToneOut, CloseCall, Waterfall |
| Key Lock | Off/On |
| Key Beep | Off/1-15/Auto |
| Volume | 0-29 |
| Squelch | 0-19 |
| Site NAC Operation | Ignore/Compare |
| Global Auto Filter | Normal/Invert/Auto/Wide Normal/Wide Invert/Wide Auto/Off |
| Audio Off Time | 0,100,500,1000,2000,3000,4000,5000,10000,30000 (ms; 0=infinite) |
| Headphone L/R output | In Phase/Invert Phase |

**SearchCommon:**

| Field | Values |
|-------|--------|
| Repeater Find | Off/On |
| Attenuator | Off/On |
| Delay | 30,10,5,4,3,2,1,0,-5,-10 |
| Modulation | Auto/AM/NFM/FM/WFM/FMB |
| agc_analog | Off/On |
| agc_digital | Off/On |
| Digital_waiting_time | 0-1000 (ms) |
| Digital_threshold_Mode | Auto/Manual/Default |
| Digital_threshold_Level | 5-13 |
| Filter | Global/Normal/Invert/Auto/Wide Normal/Wide Invert/Wide Auto/Off |

**Other profile.cfg sections:** PresetBroadcastScreen (Pager/FM/UHF TV/VHF TV/NOAA WX), CustomBroadcastScreen (10 bands with Enable/Lower/Upper), CurrentLocation (lat/lon/range), GpsOption (format/baud), InterestingLocation (name/lat/lon/range), ServiceType (PST1-37 On/Off), CustomServiceType (CST1-10 On/Off), Weather (delay/att/agc), WxSameList (SAME counties), DisplayOption (MotTgidFormat DEC/HEX, EdacTgidFormat AFS/DEC, Color Mode COLOR/BLACK/WHITE), Backlight, QuickKeys (F-Qkey.00-99 Off/On), OwnerInfo (4 message lines, max 24 chars each), ClockOption (timezone/DST), RecordingOption (replay duration/user rec), LimitSearch, CloseCall, ToneOut, Waterfall, CustomWfBand, WfColors, AvoidFreqs, IfxFreqs, BandDefault, DispOptItems, DispColors.

**SDS200-specific Backlight fields:** Dimmer_mode (Auto/Manual), Auto_Polarity (Auto+/Auto-), Manual_Level (Low/Middle/High), Dimmer_L/M/H (10-100 in steps of 10), Key_Backlight2 (Off/On), SQ Light (Off/5/10/15/OpenSquelch), Key Light (15/30/60/120/Infinite).

**LimitSearch (Custom Search Bank):**

| Field | Values |
|-------|--------|
| MyId | SrchId=xx |
| Name | Name Tag |
| Lower/Upper | Frequency format |
| Modulation | Auto/AM/NFM/FM/WFM/FMB |
| Step | Auto/5000/6250/7500/8333/10000/12500/15000/20000/25000/50000/100000 |
| Delay | 30,10,5,4,3,2,1,0,-5,-10 |
| Attenuator | Off/On |
| BankStatus | Off/On |
| Hold Time | 0-255 |
| AGC (analog/digital) | Off/On |
| Digital Waiting Time | 0-1000 |
| Digital Threshold Mode | Auto/Manual/Default |
| Digital Threshold Level | 5-13 |

**ToneOut:**

| Field | Values |
|-------|--------|
| MyId | ToneOutId=xx |
| Frequency | Frequency format |
| Modulation | AUTO/NFM/FM |
| Tone A/B | 0, 2500-35000 (1=0.1Hz) |
| Delay | 0,1,2,3,4,5,10,30,Infinite |
| Alert Tone | Off/1-9 |
| Alert Volume | Auto/1-15 |
| Alert Color | Off/Blue/Red/Magenta/Green/Cyan/Yellow/White |
| Alert Pattern | On/Slow Blink/Fast Blink |

**Waterfall:**

| Field | Values |
|-------|--------|
| Center | Frequency format |
| Modulation | Auto/AM/NFM/FM/WFM/FMB |
| Step | Auto/5000/6250/8333/10000/12500/15000/20000/25000/50000/100000 |
| Span | 0.36/0.72/1.44/2.88/5.76/8.64/17.28 (MHz) |
| FFT_Type | Line/Bar |
| FFT_Display | 25/50/75/100 |
| Max Hold | On/Off |
| Max_Hold_Time | 3/10/Infinite |
| Marker_Position | Shift/Fixed |
| rf_gain | Auto/0-15 |
| Marker_Width | Narrow/Normal/Wide |

### 6.7 f_list.cfg (Favorites List Configuration)

```
F-List	UserName	Filename	LocationControl	Monitor	Quick_key	NumberTag
	Startup_key_0	...	Startup_key_9	S-Qkey.00	...	S-Qkey.99
```

| Field | Values |
|-------|--------|
| UserName | Display name |
| Filename | File name with extension (e.g., `f_000001.hpd`) |
| LocationControl | On/Off |
| Monitor | On/Off |
| Quick key | Off/0-99 |
| NumberTag | Off/0-99 |
| Startup key 0-9 | Off/On |
| S-Qkey.00-99 | Off/On |

### 6.8 .hpd Files (Programming Data)

All `.hpd` files begin with `TargetModel\tBCDx36HP` and `FormatVersion\t<version>`.

**System types:** Conventional, Motorola, Edacs, Scat (reserve), Ltr, P25Standard (Phase I & II), P25OneFrequency, P25X2_TDMA, MotoTrbo, DmrOneFrequency, Nxdn, NxdnOneFrequency.

**Conventional System record:**

```
Conventional	MyId	ParentId	NameTag	Avoid	Reserve	SystemType
	QuickKey	NumberTag	SystemHoldTime	AnalogAGC	DigitalAGC
	DigitalWaitingTime	DigitalThresholdMode	DigitalThresholdLevel
```

**Trunk System record:**

```
Trunk	MyId	ParentId	NameTag	Avoid	Reserve	SystemType
	IDSearch	AlertTone	AlertVolume	StatusBit	NAC
	QuickKey	NumberTag	SiteHoldTime	AnalogAGC	DigitalAGC
	EndCode	PriorityIDScan	AlertColor	AlertPattern	TGIDFormat
```

| Field | Values |
|-------|--------|
| MyId | CountyId/AgencyId/TrunkId=xx |
| ParentId | StateId=xx |
| Avoid | On/Off (always Off in HPDB; managed by .avd file) |
| Quick Key | Off/0-99 |
| Number Tag | Off/0-99 |
| Hold Time | 0-255 |
| ID Search | Off/On |
| Status Bit | Yes/Ignore |
| NAC | Srch/0-FFF |
| End Code | Analog/Analog+Digital/Ignore |
| TGID Format | NEXEDGE/IDAS |

**Site record:**

```
Site	MyId	ParentId	NameTag	Avoid	Latitude	Longitude
	Range	Modulation	MotBandType	EdacsBandType
	LocationType	Attenuator	...
	DigitalWaitingTime	DigitalThresholdMode	DigitalThresholdLevel	QuickKey
	NAC	Filter
```

| Field | Values |
|-------|--------|
| Modulation | AUTO/NFM/FM |
| Mot Band Type | Standard/Sprinter/Custom |
| Edacs Band Type | Wide/Narrow |
| Location Type | Circle/Rectangles |
| Range | 1=1 mile, up to 1 decimal place |
| Filter | Global/Normal/Invert/Auto/Wide Normal/Wide Invert/Wide Auto/Off |

**C-Group (Conventional Department):**

```
C-Group	MyId	ParentId	NameTag	Avoid
	Latitude	Longitude	Range	LocationType	QuickKey	Filter
```

**T-Group (Trunk Department):**

```
T-Group	MyId	ParentId	NameTag	Avoid
	Latitude	Longitude	Range	LocationType	QuickKey
```

**C-Freq (Conventional Frequency):**

```
C-Freq	MyId	ParentId	NameTag	Avoid	Frequency	Modulation
	AudioOption	FuncTagId	Attenuator	Delay	VolumeOffset
	AlertTone	AlertVolume	AlertColor	AlertPattern
	NumberTag	PriorityChannel
```

| Field | Values |
|-------|--------|
| Frequency | Integer Hz format |
| Modulation | AUTO/AM/NFM/FM |
| AudioOption | null (All), Tone=xxxx, NAC=xxx, ColorCode=xx, RAN=xx, Area=x |
| FuncTagId | 1-37 (preset service types), 208-218 (custom) |
| Delay | 30,10,5,4,3,2,1,0,-5,-10 |
| Volume Offset | -3/-2/-1/0/1/2/3 |
| Number Tag | Off/0-999 |

**TGID record:**

```
TGID	MyId	ParentId	NameTag	Avoid	TGID	AudioType
	FuncTagId	Delay	VolumeOffset
	AlertTone	AlertVolume	AlertColor	AlertPattern
	NumberTag	PriorityChannel	TDMASlot
```

| Field | Values |
|-------|--------|
| Audio Type | ALL/ANALOG/DIGITAL |
| TDMA Slot | 1/2/Any |

**T-Freq (Trunk Frequency):**

```
T-Freq	Reserve(MyId)	ParentId	Reserve	Reserve(Avoid)	Frequency	LCN	ColorCode/RAN/Area
```

| Field | Values |
|-------|--------|
| LCN | 0, 1-30 / 1-20 / 1-4094 / 1-1023 (depends on system type) |
| ColorCode/RAN/Area | 0-15 (CC), RAN=0-63, Area=0-1, Srch |

**Other .hpd records:**

| Record | Key Fields |
|--------|------------|
| AreaState | MyId, StateId |
| AreaCounty | MyId, CountyId |
| FleetMap | MyId, B0-B7 (0/1-9/A-E) |
| UnitIds | NameTag, Unit ID (1-16777215), Alert params |
| AvoidTgids | MyId, TGID 1-16 (up to 16 per line; multiple lines if >16) |
| Rectangle | MyId, Lat1, Lon1, Lat2, Lon2 (geo-fence) |
| BandPlan_Mot | MyId, Lower/Upper/Spacing/Offset for bands 0-5 |
| BandPlan_P25 | MyId, Base/Spacing for bands 0-F |
| DQKs_Status | MyId, D-Qkey.00-99 (Off/On) |

### 6.9 discvery.cfg (Discovery Configuration)

**ConvDiscovery:**

```
ConvDiscovery	Reserve	Name	Lower	Upper	Modulation
	Step	Delay	Logging	CompareDB	Duration	TimeOut	AutoStore
```

| Field | Values |
|-------|--------|
| Name | Max 32 characters |
| Step | Auto/5000/6250/7500/8333/10000/12500/15000/20000/25000/50000/100000 |
| Delay | 5,4,3,2,1,0 (no negative delays) |
| Logging | All/New |
| Compare DB | On/Off |
| Duration | 0/30/60/90/120/150/180/300/600 (seconds) |
| Time-Out | 0/10/30/60 (0=OFF) |
| Auto Store | Off/On |

**TrunkDiscovery:**

```
TrunkDiscovery	Reserve	Name	Delay	Logging
	CompareDB	Duration	FavName	SystemName
	TrunkId	SystemType	SiteId	SiteName	TimeOut	AutoStore
```

### 6.10 .avd Files (Avoid Lists)

Four formats based on scope:

```
Avoid	StateId	SystemId	DeptId	ChannelId    (channel avoid)
Avoid	StateId	SystemId	DeptId               (department avoid)
Avoid	StateId	SystemId	SiteId               (site avoid)
Avoid	StateId	SystemId                         (system avoid)
```

### 6.11 hpdb.cfg (HomePatrol Database Config)

| Record | Format |
|--------|--------|
| DateModified | `DateModified\tTimestamp` |
| StateInfo | `StateInfo\tStateId\tCountryId\tNameTag\tShortName` |
| County | `County\tCountyId\tStateId\tNameTag` |
| LM (Locate Me) | `LM\tStateId\tCountyId\tTrunkId\tSiteId\tLM_SystemID\tLM_SiteID\tLatitude\tLongitude` |
| LM_Frequency | `LM_Frequency\tFrequency\tReserve\tLmTypeArray` (bitmask: bit0=Motorola, bit1=P25, bit2=P25 X2-TDMA) |

### 6.12 TGID Formats by System Type

| System | Normal ID | Partial ID | I-CALL ID |
|--------|-----------|------------|-----------|
| Motorola Type I | BFF-SS (standard); BFFF-S (fleet 100-127); NNNNN (size code 0) | B- (free fleet+subfleet), BFF- (free subfleet), BFFF- (fleet 100-127, free subfleet) | iNNNNN (i0-i65535) |
| Motorola Type II | NNNNN (1-65535) | -- | iNNNNN (i0-i65535) |
| P25 | NNNNN (1-65535) | -- | iNNNNNNNN (i0-i16777215) |
| DMR/MotoTRBO | NNNNNNNN (1-16777215) | -- | iNNNNNNNN (i0-i16777215) |
| NXDN | NNNNN (0-65535) | -- | iNNNNN (i0-i65535) |
| EDACS | AA-FFS (decimal 1-2047); EA Extended: NNNNN (2048-65535) | AA---- (free fleet+subfleet), AA-FF- (free subfleet) | iNNNNNN (i0-i1048575) |
| LTR | A-RR-NNN (A=0-1, RR=01-20, NNN=000-254) | A-RR---- | -- (no I-CALL) |

> Motorola Type I: B=1 digit (block), FF=2 digits zero-padded (fleet), SS=1-2 digits (subfleet). When fleet is 100-127, FFF=3 digits, S=1 digit. Type I format has no validation.
> EDACS: AA=Agency 2 digits (00-15), FF=Fleet 2 digits (00-15), S=Sub-fleet 1 digit (0-7). ALL0 (00-00-0) is invalid.
> P25, DMR, NXDN do not support partial IDs.

### 6.13 Alert Parameters (shared across file formats)

| Parameter | Values |
|-----------|--------|
| Alert Tone | Off/1-9 |
| Alert Volume | Auto/1-15 |
| Alert Light Color | Off, Blue, Red, Magenta, Green, Cyan, Yellow, White |
| Alert Light Pattern | On, Slow Blink, Fast Blink |
| Digital Waiting Time | 0-1000 (1=1ms) |
| Digital Threshold Mode | Auto, Manual, Default |
| Digital Threshold Level | 5-13 |

### 6.14 Sub Audio (SAS/SAD) Encoding

The SAS (Sub Audio Setting) and SAD (Sub Audio Detected) fields use a hex-encoded scheme shared across PSI/GSI XML and file formats. The hex value encodes the sub-audio type and value:

| Hex Range | Analog Meaning | Digital Meaning |
|-----------|---------------|-----------------|
| `All` | Tone Search (SAS) / None (SAD) | NAC Search (SAS) / None (SAD) |
| `0x0000`-`0x0FFF` | CTCSS tones (67.0 Hz through 254.1 Hz), then DCS codes | P25 NAC 000h-FFFh |
| `0x1000`-`0x100F` | CTCSS tones (continued) | DMR Color Code 0-15 |
| `0x2000`-`0x203F` | DCS codes (continued) | NXDN RAN 0-63 |
| `0x3000`-`0x3001` | DCS codes (continued) | IDAS Area 0-1 |

**CTCSS tone frequencies:** 67.0, 69.3, 71.9, 74.4, 77.0, 79.7, 82.5, 85.4, 88.5, 91.5, 94.8, 97.4, 100.0, 103.5, 107.2, 110.9, 114.8, 118.8, 123.0, 127.3, 131.8, 136.5, 141.3, 146.2, 151.4, 156.7, 159.8, 162.2, 165.5, 167.9, 171.3, 173.8, 177.3, 179.9, 183.5, 186.2, 189.9, 192.8, 196.6, 199.5, 203.5, 206.5, 210.7, 218.1, 225.7, 229.1, 233.6, 241.8, 250.3, 254.1 Hz

**DCS codes:** 006, 007, 015, 017, 021, 023, 025, 026, 031, 032, 036, 043, 047, 050, 051, 053, 054, 065, 071, 072, 073, 074, 114, 115, 116, 122, 125, 131, 132, 134, 141, 143, 145, 152, 155, 156, 162, 165, 172, 174, 205, 212, 214, 223, 225, 226, 243, 244, 245, 246, 251, 252, 255, 261, 263, 265, 266, 271, 274, 306, 311, 315, 325, 331, 332, 343, 346, 351, 356, 364, 365, 371, 411, 412, 413, 423, 431, 432, 445, 446, 452, 454, 455, 462, 464, 465, 466, 503, 506, 516, 523, 526, 532, 546, 565, 606, 612, 624, 627, 631, 632, 654, 662, 664, 703, 712, 723, 731, 732, 734, 743, 754

> See [Font Data Specification][8] for the complete SAS/SAD hex-to-tone mapping table and non-ASCII character codes for display fields.

---

## 7. Known Constraints & Implementation Notes

### Protocol-Level
- **UDP is lossy.** XML responses may span multiple packets. Always reassemble using `Footer` attributes, detect gaps via `No`, and retry on loss.
- **STS is legacy.** Use **PSI** for periodic telemetry or **GSI** for one-shot status.
- **RTSP session timeout** is 60 seconds. Send `GET_PARAMETER` keepalives.
- **Single audio client.** Only one RTSP connection is supported at a time.
- **Comma escaping.** In MSV values and Lx_CHAR fields, commas are replaced with tabs.
- **Index invalidation.** PSI/GSI Index values become invalid when DB_Counter changes. Re-query with GLT after database modifications.
- **app_data.cfg.** PC applications must delete this file when writing program data to the scanner, as the scanner reads it on resume.
- **MSB RET_LEVEL spelling.** The literal protocol string is `"RETURN_PREVOUS_MODE"` (misspelled, missing 'I'). Sending `"RETURN_PREVIOUS_MODE"` will not work.
- **KAL has no response.** Unlike all other commands, `KAL` sends no acknowledgment. Do not wait for a reply.
- **VOL range:** 0-29 (SDS200). **SQL range:** 0-19 (SDS200).
- **GST WF_MODE 0 (Normal Mode)** means the waterfall FFT fields are not meaningful — the SDS Link interface is not active in that mode.
- **GCS response terminator** is `\n` (newline), unlike every other command which terminates with `\r` (carriage return).

### XML Protocol Typos
The scanner firmware contains several misspelled XML attribute names that implementations **must** match exactly:

| Expected | Actual (in protocol) | Location |
|----------|---------------------|----------|
| `ReceiveStatus` | `ReceiveStaus` | `<LcnMonitor>` element in AST,LCN_MONITOR response |
| `TgidType` | `Tgidype` | `<CurrentActivity>` element (occurs on some entries) |
| `RETURN_PREVIOUS_MODE` | `RETURN_PREVOUS_MODE` | MSB RET_LEVEL parameter |

### Behavioral
- The `KEY,Q,P` command (squelch knob push) does not reliably exit menus remotely, unlike physical operation.
- `QSH` is invalid during Menu Mode, Direct Entry, or Quick Save operations.
- `JPM` with `0xFFFFFFFF` as channel index causes the scanner to scan from the top channel.
- `JPM` returns NG if temporary clock is set and target mode is discovery or WX alert.
- Band Scope, RF Power Plot, and Raw Data Output analyze modes are SDS200-only (not available on SDS100).
- Raw Data Output mode is available only via USB, not over the network.
- **Activity Log** (`AST,ACTIVITY_LOG`): If a temporary clock was set, the scanner returns an NG response.
- **"Unknown" department** in ID Search is a virtual department: can be held/next/prev but cannot be avoided.
- Before sending any AST command, go to Scan Mode to load the HPDB data.

---

## 8. Reference Documents

| Document | Version | Date | Description |
|----------|---------|------|-------------|
| [Virtual Serial on Network Specification][2] | V1.00 | 2019-01-23 | UDP transport, packet framing, multi-packet XML rules |
| [RTSP Specification][3] | -- | 2019-01-23 | RTSP endpoint, methods, RTP transport |
| [Remote Command Specification][6] | V1.02 | 2023-12-22 | Commands 1-30, PSI/GSI XML schema (SDS100/SDS200) |
| [SDS Series Remote Command Specification][7] | V2.00 | 2025-07-07 | Commands 1-34 + GW2/KAL; covers SDS100/SDS200/SDS150 |
| [File Specification][4] | V1.08 | 2025-10-16 | microSD structure, .hpd/.cfg file formats, TGID schemas |
| [Font Data Specification][8] | V1.00 | 2019-01-23 | Non-ASCII character codes for Lx_CHAR display fields |
| [SDS200 Waterfall Feature Operation Manual][9] | -- | -- | Waterfall feature user guide |
| [SDS200 Firmware Update Page][1] | -- | -- | Authoritative location for all specification PDFs |

[1]: https://info.uniden.com/twiki/bin/view/UnidenMan4/SDS200FirmwareUpdate
[2]: https://info.uniden.com/twiki/pub/UnidenMan4/SDS200FirmwareUpdate/SDS200_Virtual_Serial_on_Network_Specification_V1_00.pdf
[3]: https://info.uniden.com/twiki/pub/UnidenMan4/SDS200FirmwareUpdate/RTSP.pdf
[4]: https://info.uniden.com/twiki/pub/UnidenMan4/SDS200FirmwareUpdate/SDSx00_File_Specification_V1_08.pdf
[5]: https://new.marksscanners.com/SDS/100_200.shtml
[6]: https://info.uniden.com/twiki/pub/UnidenMan4/SDS200FirmwareUpdate/SDS200_RemoteCommand_Specification_V1_02.pdf
[7]: https://info.uniden.com/twiki/pub/UnidenMan4/SDS100FirmwareUpdate/SDS_Series_RemoteCommand_Specification_V2_00.pdf
[8]: https://info.uniden.com/twiki/pub/UnidenMan4/SDS200FirmwareUpdate/SDS200_FontData_V1_00.pdf
[9]: https://www.uniden.info/download/ompdf/SDS200Waterfallom.pdf
