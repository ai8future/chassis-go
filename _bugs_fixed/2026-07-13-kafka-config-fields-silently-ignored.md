# Kafka configuration fields were silently ignored

Publisher acknowledgement, compression, and linger settings plus subscriber reset, session, and poll settings were declared but not consistently wired. Constructors now validate and map supported franz-go options and reject unsupported schema-registry and remote tenant-grant settings before client creation.
