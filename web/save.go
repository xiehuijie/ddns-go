package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/jeessy2/ddns-go/v6/config"
	"github.com/jeessy2/ddns-go/v6/dns"
	"github.com/jeessy2/ddns-go/v6/util"
	passwordvalidator "github.com/wagslane/go-password-validator"
)

var startTime = time.Now().Unix()

// Save 保存
func Save(writer http.ResponseWriter, request *http.Request) {
	result := checkAndSave(request)
	dnsConfJsonStr := "[]"
	if result == "ok" {
		conf, _ := config.GetConfigCached()
		dnsConfJsonStr = getDnsConfStr(conf.DnsConf)
	}
	byt, _ := json.Marshal(map[string]string{"result": result, "dnsConf": dnsConfJsonStr})

	writer.Write(byt)
}

func checkAndSave(request *http.Request) string {
	conf, confErr := config.GetConfigCached()
	firstTime := confErr != nil

	// 从请求中读取 JSON 数据
	var data struct {
		Username           string       `json:"Username"`
		Password           string       `json:"Password"`
		NotAllowWanAccess  bool         `json:"NotAllowWanAccess"`
		WebhookURL         string       `json:"WebhookURL"`
		WebhookRequestBody string       `json:"WebhookRequestBody"`
		WebhookHeaders     string       `json:"WebhookHeaders"`
		DnsConf            []dnsConf4JS `json:"DnsConf"`
	}

	// 解析请求中的 JSON 数据
	err := json.NewDecoder(request.Body).Decode(&data)
	if err != nil {
		return util.LogStr("数据解析失败, 请刷新页面重试")
	}
	usernameNew := strings.TrimSpace(data.Username)
	passwordNew := data.Password

	// 国际化
	accept := request.Header.Get("Accept-Language")
	conf.Lang = util.InitLogLang(accept)

	// 验证安全性后才允许设置保存配置文件：
	if time.Now().Unix()-startTime > 5*60 {
		// 首次设置 && 通过外网访问 必需在服务启动的 5 分钟内
		if firstTime &&
			(!util.IsPrivateNetwork(request.RemoteAddr) || !util.IsPrivateNetwork(request.Host)) {
			return util.LogStr("若通过公网访问, 仅允许在ddns-go启动后 5 分钟内完成首次配置")
		}

		// 非首次设置 && 从未设置过帐号密码 && 本次设置了帐号或密码 必须在5分钟内
		if !firstTime &&
			(conf.Username == "" && conf.Password == "") &&
			(usernameNew != "" || passwordNew != "") {
			return util.LogStr("若从未设置过帐号密码, 仅允许在ddns-go启动后 5 分钟内设置, 请重启ddns-go")
		}
	}

	conf.NotAllowWanAccess = data.NotAllowWanAccess
	conf.Username = usernameNew
	conf.Password = passwordNew
	conf.WebhookURL = strings.TrimSpace(data.WebhookURL)
	conf.WebhookRequestBody = strings.TrimSpace(data.WebhookRequestBody)
	conf.WebhookHeaders = strings.TrimSpace(data.WebhookHeaders)

	// 如启用公网访问，帐号密码不能为空
	if !conf.NotAllowWanAccess && (conf.Username == "" || conf.Password == "") {
		return util.LogStr("启用外网访问, 必须输入登录用户名/密码")
	}

	// 如果密码不为空则检查是否够强, 内/外网要求强度不同
	if passwordNew != "" {
		var minEntropyBits float64 = 50
		if conf.NotAllowWanAccess {
			minEntropyBits = 25
		}
		err = passwordvalidator.Validate(passwordNew, minEntropyBits)
		if err != nil {
			return util.LogStr("密码不安全！尝试使用更长的密码")
		}
	}

	dnsConfFromJS := data.DnsConf
	dnsConfArray := []config.DnsConfig{}
	empty := dnsConf4JS{}
	for k, v := range dnsConfFromJS {
		if v == empty {
			continue
		}
		dnsConf := config.DnsConfig{TTL: v.TTL}
		// 覆盖以前的配置
		dnsConf.DNS.Name = v.DnsName
		dnsConf.DNS.ID = strings.TrimSpace(v.DnsID)
		dnsConf.DNS.Secret = strings.TrimSpace(v.DnsSecret)

		if v.Ipv4Domains == "" && v.Ipv6Domains == "" {
			util.Log("第 %s 个配置未填写域名", util.Ordinal(k+1, conf.Lang))
		}

		dnsConf.Ipv4.Enable = v.Ipv4Enable
		dnsConf.Ipv4.GetType = v.Ipv4GetType
		dnsConf.Ipv4.URL = strings.TrimSpace(v.Ipv4Url)
		dnsConf.Ipv4.NetInterface = v.Ipv4NetInterface
		dnsConf.Ipv4.Cmd = strings.TrimSpace(v.Ipv4Cmd)
		dnsConf.Ipv4.Domains = splitLines(v.Ipv4Domains)

		dnsConf.Ipv6.Enable = v.Ipv6Enable
		dnsConf.Ipv6.GetType = v.Ipv6GetType
		dnsConf.Ipv6.URL = strings.TrimSpace(v.Ipv6Url)
		dnsConf.Ipv6.NetInterface = v.Ipv6NetInterface
		dnsConf.Ipv6.Cmd = strings.TrimSpace(v.Ipv6Cmd)
		dnsConf.Ipv6.Ipv6Reg = strings.TrimSpace(v.Ipv6Reg)
		dnsConf.Ipv6.Domains = splitLines(v.Ipv6Domains)

		if k < len(conf.DnsConf) {
			c := &conf.DnsConf[k]
			idHide, secretHide := getHideIDSecret(c)
			if dnsConf.DNS.ID == idHide {
				dnsConf.DNS.ID = c.DNS.ID
			}
			if dnsConf.DNS.Secret == secretHide {
				dnsConf.DNS.Secret = c.DNS.Secret
			}

			// 修改cmd需要验证：必须设置帐号密码
			if (conf.Username == "" && conf.Password == "") &&
				(c.Ipv4.Cmd != dnsConf.Ipv4.Cmd || c.Ipv6.Cmd != dnsConf.Ipv6.Cmd) {
				return util.LogStr("修改 '通过命令获取' 必须设置帐号密码，请先设置帐号密码")
			}
		}

		dnsConfArray = append(dnsConfArray, dnsConf)
	}
	conf.DnsConf = dnsConfArray

	// 保存到用户目录
	err = conf.SaveConfig()

	// 只运行一次
	util.ForceCompareGlobal = true
	go dns.RunOnce()

	// 回写错误信息
	if err != nil {
		return err.Error()
	}
	return "ok"
}

// splitLines splits a string into lines by '\r\n' or '\n'.
func splitLines(s string) []string {
	if strings.Contains(s, "\r\n") {
		return strings.Split(s, "\r\n")
	}

	return strings.Split(s, "\n")
}
