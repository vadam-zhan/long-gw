package contracts

import (
	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
)

type WorkerManager interface {
	// SubmitUpstream 上行提交(Handler -> Pipeline -> WorkerManager)
	SubmitUpstream(bizCode string, msg *gateway.Message) error
}
