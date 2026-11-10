package logfile

import "golang.org/x/sys/windows"

// spaceLeft is how many bytes this user may still write on the volume dir is
// on, quotas included.
func spaceLeft(dir string) (uint64, error) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	var free, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &free, &total, &totalFree); err != nil {
		return 0, err
	}
	return free, nil
}
