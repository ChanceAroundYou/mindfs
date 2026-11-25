package config

import "mindfs/internal/configfile"

// UpdateJSONFile 保留原有名字，实现统一收口到 internal/configfile。
//
// 安装向导（cli/cmd 的 --set-node-name）也要写同一份 config.json，而它不能 import
// server/internal。两份实现一旦漂移，表现是「向导写的键服务读不到」——文件存在、
// 启动也不报错，只是设置不生效。所以实现只有一份，放在两边都能 import 的根 internal 下。
func UpdateJSONFile(path string, mutate func(map[string]any) error) error {
	return configfile.Update(path, mutate)
}
