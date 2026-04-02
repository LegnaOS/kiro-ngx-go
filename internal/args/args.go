// Package args 命令行参数解析 - 参考 src/model/arg.rs
package args

import (
	"flag"
	"os"
)

// Args 命令行参数
type Args struct {
	ConfigPath       string
	CredentialsPath  string
}

// Parse 解析命令行参数
// 支持的参数:
//   -c, --config: 配置文件路径
//   --credentials: 凭证文件路径
func Parse() *Args {
	fs := flag.NewFlagSet("kiro-proxy", flag.ContinueOnError)
	
	var configPath string
	var credentialsPath string
	
	fs.StringVar(&configPath, "config", "", "配置文件路径")
	fs.StringVar(&configPath, "c", "", "配置文件路径 (简写)")
	fs.StringVar(&credentialsPath, "credentials", "", "凭证文件路径")
	
	_ = fs.Parse(os.Args[1:])
	
	return &Args{
		ConfigPath:      configPath,
		CredentialsPath: credentialsPath,
	}
}
