// 本文件声明品牌服务所需的存储能力，由存储适配实现。
package branding

import "context"

// Repository 读写部署级品牌单例，连接与事务由实现管理。
type Repository interface {
	Read(context.Context) (Settings, error)
	// Update 在同一事务中更新指定图片并回读；失败不留下部分写入。
	Update(context.Context, Patch) (Settings, error)
}
