package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/JourneyDocker/grubdrops/internal/store/gen"
)

func TestSettings_TimeFormat_DefaultsTo24Hour(t *testing.T) {
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	s := NewSettings(gen.New(db))
	ctx := context.Background()

	// Never written → 24-hour.
	got, err := s.TimeFormat(ctx)
	require.NoError(t, err)
	assert.Equal(t, TimeFormat24, got)
}

func TestSettings_SetTimeFormat_RoundTrip(t *testing.T) {
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	s := NewSettings(gen.New(db))
	ctx := context.Background()

	require.NoError(t, s.SetTimeFormat(ctx, TimeFormat12))
	got, err := s.TimeFormat(ctx)
	require.NoError(t, err)
	assert.Equal(t, TimeFormat12, got)

	// Whitespace is trimmed on the way in.
	require.NoError(t, s.SetTimeFormat(ctx, "  24  "))
	got, _ = s.TimeFormat(ctx)
	assert.Equal(t, TimeFormat24, got)
}

func TestSettings_SetTimeFormat_RejectsInvalid(t *testing.T) {
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	s := NewSettings(gen.New(db))
	ctx := context.Background()

	require.NoError(t, s.SetTimeFormat(ctx, TimeFormat12))
	for _, bad := range []string{"", "  ", "0", "1", "24h", "am/pm", "12:00", "twelve"} {
		require.Error(t, s.SetTimeFormat(ctx, bad), "value %q must be rejected", bad)
	}
	// A rejected write leaves the stored value untouched.
	got, _ := s.TimeFormat(ctx)
	assert.Equal(t, TimeFormat12, got)
}
