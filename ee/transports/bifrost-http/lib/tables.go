// Package lib 放 ee 版 transports 的公共件. 骨架期这里放自建表的定义;
// 当插件与页面需要共用表时, 迁到 ee/framework/configstore 下对应的功能子包.
package lib

import "time"

// Probe 是骨架期用来证明 "ConfigStore.DB() 可自建表" 的探针表. ee 自建表一律 ee_ 前缀.
type Probe struct {
	ID        uint      `gorm:"primaryKey"`
	Note      string    `gorm:"size:255"`
	CreatedAt time.Time `gorm:"autoCreateTime"`
}

// TableName 固定表名, 不用 gorm 的复数推断 (否则会建成 probes).
func (Probe) TableName() string { return "ee_probe" }
