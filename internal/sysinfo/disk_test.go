package sysinfo

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		in   uint64
		want string
	}{
		{0, "0 B"},
		{1, "1 B"},
		{512, "512 B"},
		{1023, "1023 B"},
		{1024, "1 KiB"},
		{1536, "1.5 KiB"},
		{1024 * 1024, "1 MiB"},
		{5 * 1024 * 1024 * 1024, "5 GiB"},
		{3*1024*1024*1024 + 512*1024*1024, "3.5 GiB"},
		{2 * 1024 * 1024 * 1024 * 1024, "2 TiB"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, HumanBytes(c.in), "HumanBytes(%d)", c.in)
	}
}

func TestCheckDiskCurrentDir(t *testing.T) {
	// The volume holding the test working directory must exist and report
	// sane numbers on every supported platform.
	info, err := CheckDisk(".", DefaultDiskWarnPercent)
	require.NoError(t, err)
	assert.NotEmpty(t, info.Path)
	assert.Greater(t, info.TotalBytes, uint64(0))
	assert.LessOrEqual(t, info.FreeBytes, info.TotalBytes)
	assert.Equal(t, info.TotalBytes-info.FreeBytes, info.UsedBytes)
	assert.GreaterOrEqual(t, info.UsedPercent, 0.0)
	assert.LessOrEqual(t, info.UsedPercent, 100.0)
	assert.Equal(t, info.UsedPercent >= DefaultDiskWarnPercent, info.Warning)
}

func TestCheckDiskEmptyDirFallsBackToCwd(t *testing.T) {
	info, err := CheckDisk("", DefaultDiskWarnPercent)
	require.NoError(t, err)
	assert.NotEmpty(t, info.Path)
	assert.Greater(t, info.TotalBytes, uint64(0))
}

func TestCheckDiskWarningThreshold(t *testing.T) {
	// A 0% threshold must always flag a warning on any real volume.
	info, err := CheckDisk(".", 0)
	require.NoError(t, err)
	assert.True(t, info.Warning)
}
