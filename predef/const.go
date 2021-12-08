package predef

const (
	// MinIDSize is the minimum size of ID
	MinIDSize = 1
	// MaxIDSize is the maximum size of ID
	MaxIDSize = 200
	// MinSecretSize 表示 secret 长度的最小值
	MinSecretSize = MinIDSize
	// MaxSecretSize 表示 secret 长度的最大值
	MaxSecretSize = MaxIDSize
	// MaxHTTPHeaderSize max ending of host in http headers
	MaxHTTPHeaderSize = 2 * 1024
)

// OP is the type of operations
type OP = uint16

const (
	// Data is a data operation
	Data OP = iota
	// Close is a close operation
	Close
)

// VersionFirst 版本第一个组成部分
const VersionFirst byte = 0xF0
