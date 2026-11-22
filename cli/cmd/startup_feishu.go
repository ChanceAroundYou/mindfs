package main

import (
	"fmt"
	"strings"
	"time"

	"mindfs/server/app"
)

// startupChannelsConfig 是 config.json 里的 `channels` 段。M1 只读飞书出口，
// 未知键原样忽略 —— M2/M4 加字段时不需要迁移。
type startupChannelsConfig struct {
	Feishu *startupFeishuConfig `json:"feishu"`
}

// startupFeishuBinding 是 bindings 数组里的一项。
type startupFeishuBinding struct {
	ChatID string `json:"chat_id"`
	NodeID string `json:"node_id"`
	RootID string `json:"root_id"`
}

// startupFeishuConfig 是飞书通道的本机配置。这里没有任何凭据：app secret 只在
// <home>/.lark-cli 里，由 lark-cli 自己读。
type startupFeishuConfig struct {
	Enabled          *bool                  `json:"enabled"`
	OwnerOpenID      *string                `json:"owner_open_id"`
	LarkCLI          *string                `json:"lark_cli"`
	CardTTLHours     *int                   `json:"card_ttl_hours"`
	Bindings         []startupFeishuBinding `json:"bindings"`
	DefaultRootID    *string                `json:"default_root_id"`
	PickerTTLHours   *int                   `json:"project_picker_ttl_hours"`
	PickerMax        *int                   `json:"project_picker_max"`
	InboundPerMinute *int                   `json:"inbound_per_minute"`
	InboundBurst     *int                   `json:"inbound_burst"`
	RelayToken       *string                `json:"relay_token"`
}

// relayTokenMinLength 是共享密钥的最短长度；空串表示跨节点中继关闭（fail closed）。
const relayTokenMinLength = 32

// applyFeishuConfig 把 channels.feishu 抄进 flag 变量。通道没有命令行开关，
// explicit 永远不含这些键；保留同样的写法是为了与其余配置段一致。
func applyFeishuConfig(cfg startupConfig, explicit map[string]bool, flags startupFlags) error {
	if cfg.Channels == nil || cfg.Channels.Feishu == nil {
		return nil
	}
	f := cfg.Channels.Feishu
	if flags.FeishuEnabled != nil && f.Enabled != nil && !explicit["feishu-enabled"] {
		*flags.FeishuEnabled = *f.Enabled
	}
	if flags.FeishuOwnerOpenID != nil && f.OwnerOpenID != nil && !explicit["feishu-owner-open-id"] {
		*flags.FeishuOwnerOpenID = strings.TrimSpace(*f.OwnerOpenID)
	}
	if flags.FeishuLarkCLI != nil && f.LarkCLI != nil && !explicit["feishu-lark-cli"] {
		*flags.FeishuLarkCLI = strings.TrimSpace(*f.LarkCLI)
	}
	if flags.FeishuCardTTLHours != nil && f.CardTTLHours != nil && !explicit["feishu-card-ttl-hours"] {
		*flags.FeishuCardTTLHours = *f.CardTTLHours
	}
	if flags.FeishuBindings != nil {
		bindings := make([]app.FeishuBinding, 0, len(f.Bindings))
		for _, binding := range f.Bindings {
			bindings = append(bindings, app.FeishuBinding{
				ChatID: strings.TrimSpace(binding.ChatID),
				NodeID: strings.TrimSpace(binding.NodeID),
				RootID: strings.TrimSpace(binding.RootID),
			})
		}
		*flags.FeishuBindings = bindings
	}
	if flags.FeishuDefaultRootID != nil && f.DefaultRootID != nil {
		*flags.FeishuDefaultRootID = strings.TrimSpace(*f.DefaultRootID)
	}
	if flags.FeishuPickerTTLHours != nil && f.PickerTTLHours != nil {
		*flags.FeishuPickerTTLHours = *f.PickerTTLHours
	}
	if flags.FeishuPickerMax != nil && f.PickerMax != nil {
		*flags.FeishuPickerMax = *f.PickerMax
	}
	if flags.FeishuInboundPerMinute != nil && f.InboundPerMinute != nil {
		*flags.FeishuInboundPerMinute = *f.InboundPerMinute
	}
	if flags.FeishuInboundBurst != nil && f.InboundBurst != nil {
		*flags.FeishuInboundBurst = *f.InboundBurst
	}
	if flags.FeishuRelayToken != nil && f.RelayToken != nil {
		token := strings.TrimSpace(*f.RelayToken)
		if token != "" && len(token) < relayTokenMinLength {
			return fmt.Errorf("channels.feishu.relay_token 至少 %d 字符", relayTokenMinLength)
		}
		*flags.FeishuRelayToken = token
	}
	return nil
}

// feishuOptions 把已落盘的配置收敛成启动参数，并在这里补齐缺省值。
// 只读 flags 变量：config.json 的抄写已在 applyFeishuConfig 完成。
//
// 缺省关闭是硬规则：没有配置飞书的人不该因为这次改动多出一个后台常驻进程。
// 只配了 owner 而漏了 lark_cli / ttl 时，补上缺省值让通道能起来 —— 缺省值
// 错了只是发不出去，会在卡片上留下错误，比整个通道静默不启动更容易发现。
func feishuOptions(flags startupFlags) app.FeishuOptions {
	var opts app.FeishuOptions
	if flags.FeishuEnabled != nil {
		opts.Enabled = *flags.FeishuEnabled
	}
	if flags.FeishuOwnerOpenID != nil {
		opts.OwnerOpenID = strings.TrimSpace(*flags.FeishuOwnerOpenID)
	}
	if flags.FeishuLarkCLI != nil {
		opts.LarkCLIPath = strings.TrimSpace(*flags.FeishuLarkCLI)
	}
	if opts.LarkCLIPath == "" {
		opts.LarkCLIPath = "lark-cli"
	}
	hours := 0
	if flags.FeishuCardTTLHours != nil {
		hours = *flags.FeishuCardTTLHours
	}
	if hours <= 0 {
		hours = 168
	}
	opts.CardTTL = time.Duration(hours) * time.Hour
	if flags.FeishuBindings != nil {
		opts.Bindings = *flags.FeishuBindings
	}
	if flags.FeishuDefaultRootID != nil {
		opts.DefaultRootID = strings.TrimSpace(*flags.FeishuDefaultRootID)
	}
	pickerHours := 0
	if flags.FeishuPickerTTLHours != nil {
		pickerHours = *flags.FeishuPickerTTLHours
	}
	if pickerHours <= 0 {
		pickerHours = 24
	}
	opts.PickerTTL = time.Duration(pickerHours) * time.Hour
	opts.PickerMax = 5
	if flags.FeishuPickerMax != nil && *flags.FeishuPickerMax > 0 {
		opts.PickerMax = *flags.FeishuPickerMax
	}
	opts.InboundRatePerMinute = 30
	if flags.FeishuInboundPerMinute != nil && *flags.FeishuInboundPerMinute > 0 {
		opts.InboundRatePerMinute = *flags.FeishuInboundPerMinute
	}
	opts.InboundBurst = 10
	if flags.FeishuInboundBurst != nil && *flags.FeishuInboundBurst > 0 {
		opts.InboundBurst = *flags.FeishuInboundBurst
	}
	if flags.FeishuRelayToken != nil {
		opts.RelayToken = strings.TrimSpace(*flags.FeishuRelayToken)
	}
	return opts
}
