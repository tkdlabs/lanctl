package api

import (
	"net/http"
	"time"

	lhttphandler "github.com/tkdlabs/lanctl/internal/httphandler"
	"github.com/tkdlabs/lanctl/internal/notices"
	"github.com/tkdlabs/lanctl/internal/version"
)

// GET /api/notices
func Notices(w http.ResponseWriter, r *http.Request) {
	lhttphandler.JSON(w, http.StatusOK, notices.Build(version.Version, time.Now()))
}
