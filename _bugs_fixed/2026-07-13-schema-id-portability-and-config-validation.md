# Schema IDs and config validation failed open at type boundaries

Schema IDs above the Confluent uint32 wire range could truncate, and uint32 IDs
above the runtime `int` range could wrap on 32-bit builds. Config validation
also ignored unknown operators and treated incompatible numeric values as zero.

Schema registration, serialization, and deserialization now enforce the
portable supported range before conversion. Config checks now reject malformed,
unknown, and type-incompatible rules with field/operator diagnostics while
preserving valid numeric aliases, strings, and durations.
