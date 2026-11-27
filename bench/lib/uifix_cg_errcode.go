package lib

import (
	"encoding/json"

	"github.com/maximhq/bifrost/bench/plaintext"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Error codes (ErrorField.Code, fix wave M1-44).
const (
	ErrCodeNoModelKey     = "no_model_key"
	ErrCodeBudgetReached  = "budget_reached"
	ErrCodeWorkerRefused  = "worker_refused"
	ErrCodeImagePreparing = "image_preparing"
)

// SendErrorCode is SendErrorLink with an error code beside the type. link
// may be empty.
func SendErrorCode(ctx *fasthttp.RequestCtx, mode schemas.Mode, status int, errType, code, message, link string) {
	message = plaintext.Hosted(message)
	body, err := json.Marshal(ErrorBody{Mode: mode, Error: ErrorField{Message: message, Type: errType, Link: link, Code: code}})
	if err != nil {
		ctx.Error("internal error", fasthttp.StatusInternalServerError)
		return
	}
	writeJSON(ctx, mode, status, body)
}
