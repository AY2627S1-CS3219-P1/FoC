package health

import (
	"net/http"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/deps"
)

func HandleCheckHealth(r *http.Request, env *deps.Env) (*api.Response, error) {
	return api.NewResponse(map[string]string{"status": "ok"})
}
