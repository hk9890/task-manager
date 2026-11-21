# 0002: One engine is the only code that reads or writes a store

One owner of all store files is what makes it possible to validate every write and to serialize
all writers under one lock. Every other invariant of the store rests on it. The CLI and every
other front end call the engine as a library, in the same process.
