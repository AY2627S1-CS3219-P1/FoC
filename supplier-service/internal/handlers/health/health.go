package health

import (
	"net/http"

	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/api"
)

func HandleCheckHealth(r *http.Request, env *api.Env) (*api.Response, error) {
	return api.NewResponse(map[string]string{"status": "ok"})
}
