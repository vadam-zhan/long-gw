package types

import (
    pb "github.com/vadam-zhan/long-gw/common-protocol/v1"
)

// BusinessType 业务类型
type BusinessType int8

const (
    BusinessTypeUnknown BusinessType = 0
    BusinessTypeIM      BusinessType = 1
    BusinessTypeLIVE    BusinessType = 2
    BusinessTypeMESSAGE BusinessType = 3
    BusinessTypePUSH    BusinessType = 4
)

// Proto 转换为 proto 枚举
func (bt BusinessType) Proto() pb.BusinessType {
    return pb.BusinessType(bt)
}

// FromProto 从 proto 枚举转换
func BusinessTypeFromProto(p pb.BusinessType) BusinessType {
    return BusinessType(p)
}

// String 返回字符串
func (bt BusinessType) String() string {
    switch bt {
    case BusinessTypeIM:
        return "im"
    case BusinessTypeLIVE:
        return "live"
    case BusinessTypeMESSAGE:
        return "message"
    case BusinessTypePUSH:
        return "push"
    default:
        return "unknown"
    }
}