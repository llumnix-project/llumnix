package handler

import (
	"llumnix/pkg/types"
)

func writeErrorResponse(req *types.RequestContext, err error) {
	req.ResponseChan <- &types.ResponseMsg{Err: err}
}

func writeResponse(req *types.RequestContext, chunk []byte) {
	req.ResponseChan <- &types.ResponseMsg{Err: nil, Message: chunk}
}
