Multi-Chain Token Radar V2.5 Source
===================================

main.go       Windows Win32 GUI、网络、评分、缓存和事件循环
multichain.go Base/BSC/Arbitrum 模块、RPC、新池发现、公平轮询和多链诊断
sim.go        多链模拟账户、三档自动策略、成本、止损止盈和统计
sim_test.go   模拟系统单元测试

构建：
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-H=windowsgui -s -w" -o BaseTokenRadar.exe .
