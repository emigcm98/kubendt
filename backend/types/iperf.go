package types

// IperfInterval is one reporting interval of a throughput test, the client's
// view, summed over its streams.
type IperfInterval struct {
	Start         float64 `json:"start" example:"0"`
	End           float64 `json:"end" example:"1.0"`
	Bytes         int64   `json:"bytes" example:"12582912"`
	BitsPerSecond float64 `json:"bitsPerSecond" example:"100663296"`
	// TCP: retransmissions the sender saw in the interval.
	Retransmits *int64 `json:"retransmits,omitempty" example:"0"`
	// UDP: datagrams sent in the interval.
	Packets *int64 `json:"packets,omitempty" example:"86"`
}

// IperfSummary is the end report of a throughput test. Sent is the sender's
// side and received the receiver's, whichever end they are (reverse swaps
// them). Jitter and loss exist for UDP only.
type IperfSummary struct {
	Seconds               float64  `json:"seconds" example:"10.0"`
	SentBytes             int64    `json:"sentBytes" example:"125829120"`
	SentBitsPerSecond     float64  `json:"sentBitsPerSecond" example:"100663296"`
	ReceivedBytes         int64    `json:"receivedBytes" example:"125829120"`
	ReceivedBitsPerSecond float64  `json:"receivedBitsPerSecond" example:"100663296"`
	Retransmits           *int64   `json:"retransmits,omitempty" example:"3"`
	JitterMs              *float64 `json:"jitterMs,omitempty" example:"0.021"`
	Packets               *int64   `json:"packets,omitempty" example:"860"`
	LostPackets           *int64   `json:"lostPackets,omitempty" example:"4"`
	LostPercent           *float64 `json:"lostPercent,omitempty" example:"0.46"`
	CPUClientPercent      float64  `json:"cpuClientPercent" example:"3.2"`
	CPUServerPercent      float64  `json:"cpuServerPercent" example:"2.9"`
}

// IperfServerDoc says where the test's server ran, when the backend ran it.
type IperfServerDoc struct {
	Pod  string `json:"pod" example:"web-0"`
	Port int    `json:"port" example:"5312"`
}

// IperfReportResponse is returned by GET /iperf/run/:namespace/:podName.
type IperfReportResponse struct {
	Source      string          `json:"source" example:"host-0"`
	Destination string          `json:"destination" example:"192.168.3.10"`
	Proto       string          `json:"proto" example:"tcp"`
	Duration    int             `json:"duration" example:"10"`
	Bandwidth   string          `json:"bandwidth,omitempty" example:"10M"`
	Parallel    int             `json:"parallel" example:"1"`
	Reverse     bool            `json:"reverse" example:"false"`
	Server      *IperfServerDoc `json:"server,omitempty"`
	StartedAt   string          `json:"startedAt" example:"2026-10-15T14:30:00Z"`
	FinishedAt  string          `json:"finishedAt" example:"2026-10-15T14:30:11Z"`
	Intervals   []IperfInterval `json:"intervals"`
	Summary     *IperfSummary   `json:"summary"`
}
