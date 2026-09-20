package health

import (
	"net/http"

	"github.com/yihao03/reminding/internal/api"
)

func HandleCheckHealth(r *http.Request, env *api.Env) (*api.Response, error) {
	return api.NewResponse(map[string]string{"status": "ok"})
}
