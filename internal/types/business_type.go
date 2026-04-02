package types

import (
	"encoding/json"
	"fmt"

	pb "github.com/vadam-zhan/long-gw/api/proto/v1"
)

// BusinessType 业务类型，支持 JSON/YAML 反序列化
type BusinessType string

const (
	BusinessTypeUnknown BusinessType = "unknown"
	BusinessTypeIM      BusinessType = "im"
	BusinessTypeLIVE    BusinessType = "live"
	BusinessTypeMESSAGE BusinessType = "message"
)

// Proto 转换为 proto 枚举
func (bt BusinessType) Proto() pb.BusinessType {
	switch bt {
	case BusinessTypeIM:
		return pb.BusinessType_BusinessType_IM
	case BusinessTypeLIVE:
		return pb.BusinessType_BusinessType_LIVE
	case BusinessTypeMESSAGE:
		return pb.BusinessType_BusinessType_MESSAGE
	default:
		return pb.BusinessType_BusinessType_UNSPECIFIED
	}
}

// Proto 转换为 business type 枚举
func Proto(bt string) string {
	switch bt {
	case pb.BusinessType_BusinessType_IM.String():
		return BusinessTypeIM.String()
	case pb.BusinessType_BusinessType_LIVE.String():
		return BusinessTypeLIVE.String()
	case pb.BusinessType_BusinessType_MESSAGE.String():
		return BusinessTypeMESSAGE.String()
	}
	return BusinessTypeUnknown.String()
}

// Validate 校验是否为有效的业务类型
func (bt BusinessType) Validate() error {
	switch bt {
	case BusinessTypeIM, BusinessTypeLIVE, BusinessTypeMESSAGE:
		return nil
	case "":
		return fmt.Errorf("business type cannot be empty")
	default:
		return fmt.Errorf("unknown business type: %s, valid values: im, live, message", bt)
	}
}

// String 返回字符串表示
func (bt BusinessType) String() string {
	return string(bt)
}

// UnmarshalJSON 实现 json.Unmarshaler
func (bt *BusinessType) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	*bt = BusinessType(s)
	return bt.Validate()
}

// AllBusinessTypes 返回所有已定义的业务类型
func AllBusinessTypes() []BusinessType {
	return []BusinessType{BusinessTypeIM, BusinessTypeLIVE, BusinessTypeMESSAGE}
}
