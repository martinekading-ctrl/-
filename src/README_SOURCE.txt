Multi-Chain Token Radar V2.21 Source
===================================

main.go       Windows Win32 GUI、网络、评分、缓存和事件循环
multichain.go 六链模块、RPC、新池发现、严格安全门槛、精确池跟踪和来源退避
realtime.go   Base/BSC/Arbitrum 免费 WebSocket 建池事件、断点队列、健康状态和 HTTP 回补衔接
sim.go        多链模拟账户、五档自动策略、报价完整性保护、常数乘积滑点和策略指纹
sim_test.go   模拟系统单元测试与报价中断保护回归测试

构建：
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-H=windowsgui -s -w" -o BaseTokenRadar.exe .
