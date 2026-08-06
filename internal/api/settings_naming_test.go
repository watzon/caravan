package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/watzon/caravan/internal/store"
)

func TestPutSettingsNamingAndRecycleValidation(t *testing.T) {
	h, st, _ := newTestServer(t)
	valid := `{"recycle_retention_days":"30","movie_folder_format":"{year} - {title}","episode_file_format":"{series} {episode}"}`
	wantStatus(t, do(t, h, http.MethodPut, "/api/v1/settings", valid), http.StatusOK)
	if got, err := st.GetSetting(context.Background(), store.SettingRecycleRetentionDays); err != nil || got != "30" {
		t.Fatalf("recycle retention = %q, %v, want 30", got, err)
	}

	invalid := `{"recycle_retention_days":"3651","movie_folder_format":"{edition}"}`
	wantStatus(t, do(t, h, http.MethodPut, "/api/v1/settings", invalid), http.StatusBadRequest)
	if got, err := st.GetSetting(context.Background(), store.SettingRecycleRetentionDays); err != nil || got != "30" {
		t.Fatalf("invalid save changed recycle retention to %q, %v", got, err)
	}
	if got, err := st.GetSetting(context.Background(), store.SettingMovieFolderFormat); err != nil || got != "{year} - {title}" {
		t.Fatalf("invalid save changed movie format to %q, %v", got, err)
	}
}
