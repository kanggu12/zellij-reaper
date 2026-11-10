package logfile

// dirWritable says why a file could not be made in dir, or nil. Windows
// decides that by access lists a mode bit does not show, and asking would mean
// making a file: a directory is taken as writable, and a log that is there is
// still checked by opening it.
func dirWritable(string) error { return nil }
