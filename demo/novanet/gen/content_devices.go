// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"strings"
)

// Device guides: routers (R3, R5, R5 Pro), optical modems (G1, G2), TV boxes
// (T2, T2 Max) and the mesh extender (M1). Each guide has the same shape and a
// different set of figures, so questions about R5 and R5 Pro (or T2 and T2
// Max) have near-identical documents that differ in one detail.

type device struct {
	slug, name, nameZH string
	kindEN, kindZH     string
	// spec rows: label EN, label ZH, value EN, value ZH
	specs      [][4]string
	resetSecs  int
	resetHow   string // EN description of the control
	resetHowZH string
	setupEN    string
	setupZH    string
	ledEN      string // one line per LED, "name | meaning"
	ledZH      string
	withEN     string
	withZH     string
	extraEN    string
	extraZH    string
}

var devices = []device{
	{
		slug: "r3", name: "R3", nameZH: "R3", kindEN: "Wi-Fi 5 router", kindZH: "Wi-Fi 5路由器",
		specs: [][4]string{
			{"Wi-Fi standard", "无线标准", "Wi-Fi 5, dual band", "Wi-Fi 5，双频"},
			{"LAN ports", "网口", "4 gigabit ports", "4个千兆网口"},
			{"Coverage", "覆盖面积", "up to 80 square meters", "最大约80平方米"},
			{"Connected devices", "连接设备数", "up to 64", "最多64台"},
		},
		resetSecs: 5, resetHow: "the Reset pinhole on the back", resetHowZH: "背面的Reset小孔",
		setupEN: "Connect the optical modem to the WAN port of the R3 with the yellow cable, plug in the power and wait 3 minutes. The Internet light turns green when the R3 is online.",
		setupZH: "用黄色网线把光猫连到R3的WAN口，接通电源，等待3分钟。R3联网成功后，Internet灯变绿。",
		ledEN:   "Power | Green means the router is on\nInternet | Green means online, red means no link to the modem\nWi-Fi | Green means Wi-Fi is on",
		ledZH:   "Power | 绿灯表示已开机\nInternet | 绿灯表示已联网，红灯表示与光猫没有连接\nWi-Fi | 绿灯表示无线已开启",
		withEN:  "The R3 comes with Fiber 500. It does not support the M1 mesh extender.",
		withZH:  "R3随光纤500赠送，不支持M1扩展器组网。",
		extraEN: "The R3 is a Wi-Fi 5 router and is the entry model of the NovaNet router range.", extraZH: "R3是Wi-Fi 5路由器，是星联宽带路由器系列的入门型号。",
	},
	{
		slug: "r5", name: "R5", nameZH: "R5", kindEN: "Wi-Fi 6 router", kindZH: "Wi-Fi 6路由器",
		specs: [][4]string{
			{"Wi-Fi standard", "无线标准", "Wi-Fi 6, dual band", "Wi-Fi 6，双频"},
			{"LAN ports", "网口", "4 gigabit ports", "4个千兆网口"},
			{"Coverage", "覆盖面积", "up to 120 square meters", "最大约120平方米"},
			{"Connected devices", "连接设备数", "up to 128", "最多128台"},
		},
		resetSecs: 8, resetHow: "the Reset button under the label", resetHowZH: "标签下方的Reset按钮",
		setupEN: "Connect the optical modem to the blue WAN port of the R5 and plug in the power. Wait 2 minutes until the Internet light is steady green, then scan the QR code on the label with the NovaNet app to name your Wi-Fi.",
		setupZH: "把光猫连到R5的蓝色WAN口，接通电源，等待2分钟，Internet灯绿灯常亮后，用星联宽带应用扫描标签上的二维码，设置Wi-Fi名称。",
		ledEN:   "Power | Green means the router is on\nInternet | Green means online, red means no link to the modem\nWi-Fi | Green means Wi-Fi 6 is on, blinking means pairing",
		ledZH:   "Power | 绿灯表示已开机\nInternet | 绿灯表示已联网，红灯表示与光猫没有连接\nWi-Fi | 绿灯表示Wi-Fi 6已开启，闪烁表示正在配对",
		withEN:  "The R5 is sold separately as an upgrade. It works with up to 2 M1 mesh extenders.",
		withZH:  "R5可单独购买升级，最多可搭配2个M1扩展器组网。",
		extraEN: "The R5 suits flats and small houses.", extraZH: "R5适合公寓和小户型住宅。",
	},
	{
		slug: "r5-pro", name: "R5 Pro", nameZH: "R5 Pro", kindEN: "Wi-Fi 6 router with a 2.5G port", kindZH: "带2.5G网口的Wi-Fi 6路由器",
		specs: [][4]string{
			{"Wi-Fi standard", "无线标准", "Wi-Fi 6, tri band", "Wi-Fi 6，三频"},
			{"LAN ports", "网口", "1 port of 2.5 gigabit and 4 gigabit ports", "1个2.5G网口和4个千兆网口"},
			{"Coverage", "覆盖面积", "up to 160 square meters", "最大约160平方米"},
			{"Connected devices", "连接设备数", "up to 256", "最多256台"},
		},
		resetSecs: 10, resetHow: "the Reset button under the label", resetHowZH: "标签下方的Reset按钮",
		setupEN: "Connect the G2 optical modem to the 2.5G WAN port of the R5 Pro and plug in the power. Wait 2 minutes until the Internet light is steady green, then scan the QR code on the label with the NovaNet app.",
		setupZH: "把G2光猫连到R5 Pro的2.5G WAN口，接通电源，等待2分钟，Internet灯绿灯常亮后，用星联宽带应用扫描标签上的二维码。",
		ledEN:   "Power | Green means the router is on\nInternet | Green means online, red means no link to the modem\nWi-Fi | Green means Wi-Fi 6 is on, blinking means pairing\nMesh | Blue means an M1 extender is paired",
		ledZH:   "Power | 绿灯表示已开机\nInternet | 绿灯表示已联网，红灯表示与光猫没有连接\nWi-Fi | 绿灯表示Wi-Fi 6已开启，闪烁表示正在配对\nMesh | 蓝灯表示已与M1扩展器配对",
		withEN:  "The R5 Pro comes with Fiber 1000. It works with up to 4 M1 mesh extenders.",
		withZH:  "R5 Pro随光纤1000赠送，最多可搭配4个M1扩展器组网。",
		extraEN: "The R5 Pro is the top model of the NovaNet router range and the only one with a 2.5G port.", extraZH: "R5 Pro是星联宽带路由器系列的旗舰型号，也是唯一带2.5G网口的型号。",
	},
	{
		slug: "g1", name: "G1", nameZH: "G1", kindEN: "optical modem", kindZH: "光猫",
		specs: [][4]string{
			{"Port speed", "网口速率", "1 gigabit", "千兆"},
			{"Supported plans", "支持的套餐", "up to Fiber 500", "最高支持光纤500"},
			{"Fiber connector", "光纤接口", "SC/APC", "SC/APC"},
		},
		resetSecs: 3, resetHow: "the Reset pinhole on the side", resetHowZH: "侧面的Reset小孔",
		setupEN: "Connect the fiber cable to the PON port of the G1 and plug in the power. The PON light turns green within 5 minutes when the line is ready.",
		setupZH: "把光纤接到G1的PON口，接通电源，线路正常时PON灯会在5分钟内变绿。",
		ledEN:   "PON | Green means the fiber line is registered\nLOS | Red blinking means no light signal on the fiber\nLAN | Green means a device is connected",
		ledZH:   "PON | 绿灯表示光纤线路已注册\nLOS | 红灯闪烁表示光纤没有光信号\nLAN | 绿灯表示已连接设备",
		withEN:  "The G1 is installed with Fiber 500. Fiber 1000 needs the G2 instead.",
		withZH:  "光纤500安装G1光猫；光纤1000需要改用G2光猫。",
		extraEN: "The G1 is the standard optical modem.", extraZH: "G1是标准光猫。",
	},
	{
		slug: "g2", name: "G2", nameZH: "G2", kindEN: "10G-ready optical modem", kindZH: "万兆光猫",
		specs: [][4]string{
			{"Port speed", "网口速率", "2.5 gigabit", "2.5G"},
			{"Supported plans", "支持的套餐", "up to Fiber 1000", "最高支持光纤1000"},
			{"Fiber connector", "光纤接口", "SC/APC", "SC/APC"},
		},
		resetSecs: 6, resetHow: "the Reset pinhole on the side", resetHowZH: "侧面的Reset小孔",
		setupEN: "Connect the fiber cable to the PON port of the G2 and plug in the power. The PON light turns green within 5 minutes. Use the 2.5G LAN port to connect the router.",
		setupZH: "把光纤接到G2的PON口，接通电源，PON灯会在5分钟内变绿。用2.5G网口连接路由器。",
		ledEN:   "PON | Green means the fiber line is registered\nLOS | Red blinking means no light signal on the fiber\nLAN | Green means a device is connected",
		ledZH:   "PON | 绿灯表示光纤线路已注册\nLOS | 红灯闪烁表示光纤没有光信号\nLAN | 绿灯表示已连接设备",
		withEN:  "The G2 is required for Fiber 1000 and is fitted by the technician.",
		withZH:  "光纤1000必须使用G2光猫，由师傅上门安装。",
		extraEN: "The G2 is needed to reach the full Fiber 1000 speed.", extraZH: "只有G2才能跑满光纤1000的速率。",
	},
	{
		slug: "t2", name: "T2", nameZH: "T2", kindEN: "4K TV box", kindZH: "4K电视盒",
		specs: [][4]string{
			{"Video", "视频", "4K at 30 frames per second", "4K，每秒30帧"},
			{"Wi-Fi", "无线", "Wi-Fi 5", "Wi-Fi 5"},
			{"Storage", "存储空间", "16 GB", "16GB"},
			{"Remote", "遥控器", "infrared remote with a voice button", "红外遥控器，带语音键"},
		},
		resetSecs: 5, resetHow: "the Home and Back buttons together", resetHowZH: "同时按住Home键和Back键",
		setupEN: "Connect the T2 to the TV with the HDMI cable, plug in the power and follow the on-screen steps. Choose your Wi-Fi or plug in a network cable.",
		setupZH: "用HDMI线把T2连到电视，接通电源，按屏幕提示操作，选择Wi-Fi或插上网线。",
		ledEN:   "Status light | White means on, blinking white means starting up, red means no network",
		ledZH:   "状态灯 | 白灯表示已开机，白灯闪烁表示正在启动，红灯表示没有网络",
		withEN:  "One T2 is included with Fiber 1000. With Fiber 500 it can be added for 10 yuan per month.",
		withZH:  "光纤1000含一台T2；光纤500可另加，每月10元。",
		extraEN: "The T2 plays NovaNet TV and the common video apps.", extraZH: "T2可以播放星联电视和常见的视频应用。",
	},
	{
		slug: "t2-max", name: "T2 Max", nameZH: "T2 Max", kindEN: "4K HDR TV box", kindZH: "4K HDR电视盒",
		specs: [][4]string{
			{"Video", "视频", "4K HDR at 60 frames per second", "4K HDR，每秒60帧"},
			{"Wi-Fi", "无线", "Wi-Fi 6", "Wi-Fi 6"},
			{"Storage", "存储空间", "64 GB", "64GB"},
			{"Remote", "遥控器", "Bluetooth voice remote", "蓝牙语音遥控器"},
		},
		resetSecs: 10, resetHow: "the Home and Back buttons together", resetHowZH: "同时按住Home键和Back键",
		setupEN: "Connect the T2 Max to the TV with the HDMI 2.1 cable, plug in the power and pair the Bluetooth remote by holding the Home button for 3 seconds.",
		setupZH: "用HDMI 2.1线把T2 Max连到电视，接通电源，按住Home键3秒配对蓝牙遥控器。",
		ledEN:   "Status light | White means on, blinking white means starting up, red means no network",
		ledZH:   "状态灯 | 白灯表示已开机，白灯闪烁表示正在启动，红灯表示没有网络",
		withEN:  "The T2 Max is sold separately.",
		withZH:  "T2 Max需单独购买。",
		extraEN: "The T2 Max is the premium TV box of the range.", extraZH: "T2 Max是电视盒系列的高端型号。",
	},
	{
		slug: "m1", name: "M1", nameZH: "M1", kindEN: "mesh extender", kindZH: "Mesh扩展器",
		specs: [][4]string{
			{"Wi-Fi standard", "无线标准", "Wi-Fi 6", "Wi-Fi 6"},
			{"Extra coverage", "新增覆盖", "about 60 square meters each", "每个约60平方米"},
			{"Works with", "适配路由器", "R5 and R5 Pro", "R5和R5 Pro"},
		},
		resetSecs: 5, resetHow: "the Pair button", resetHowZH: "Pair按键",
		setupEN: "Plug the M1 into a socket within 10 meters of the router. Press the Pair button on the router, then press the Pair button on the M1 within 2 minutes. The light turns solid blue when they are paired.",
		setupZH: "把M1插在距离路由器10米以内的插座上，先按路由器上的Pair键，2分钟内再按M1的Pair键，配对成功后指示灯蓝灯常亮。",
		ledEN:   "Status light | Blue means paired, blinking blue means pairing, red means the router is out of reach",
		ledZH:   "状态灯 | 蓝灯表示已配对，蓝灯闪烁表示正在配对，红灯表示距离路由器太远",
		withEN:  "The M1 works with the R5 and the R5 Pro only. It does not work with the R3.",
		withZH:  "M1只适配R5和R5 Pro，不能用于R3。",
		extraEN: "The M1 extends Wi-Fi to rooms that the router does not reach.", extraZH: "M1用来把Wi-Fi延伸到路由器覆盖不到的房间。",
	},
}

func ledTable(s string) string {
	var sb strings.Builder
	for _, l := range strings.Split(s, "\n") {
		parts := strings.SplitN(l, " | ", 2)
		fmt.Fprintf(&sb, "| %s | %s |\n", parts[0], parts[1])
	}
	return sb.String()
}

func init() {
	for i, d := range devices {
		var specEN, specZH strings.Builder
		for _, s := range d.specs {
			fmt.Fprintf(&specEN, "| %s | %s |\n", s[0], s[2])
			fmt.Fprintf(&specZH, "| %s | %s |\n", s[1], s[3])
		}
		resetEN := fmt.Sprintf("To reset the %s to factory settings, hold %s for %d seconds until the lights blink. Resetting erases your Wi-Fi name and password.", d.name, d.resetHow, d.resetSecs)
		resetZH := fmt.Sprintf("要把%s恢复出厂设置，请按住%s %d秒，直到指示灯闪烁。恢复出厂设置会清除您的Wi-Fi名称和密码。", d.nameZH, d.resetHowZH, d.resetSecs)
		en := fmt.Sprintf(`# NovaNet %[1]s user guide
Model: %[1]s
Type: %[2]s
## What is the %[1]s?
%[3]s
## How do I set up the %[1]s?
%[5]s
## What do the lights on the %[1]s mean?
| Light | Meaning |
%[6]s## How do I reset the %[1]s?
%[7]s
## What are the %[1]s specifications?
| Item | Value |
%[8]s## Which plans use the %[1]s?
%[9]s
`, d.name, d.kindEN, d.extraEN, "", d.setupEN, ledTable(d.ledEN), resetEN, specEN.String(), d.withEN)
		zh := fmt.Sprintf(`# 星联%[1]s使用指南
型号：%[1]s
类型：%[2]s
## %[1]s是什么？
%[3]s
## %[1]s怎么安装？
%[5]s
## %[1]s的指示灯是什么意思？
| 指示灯 | 含义 |
%[6]s## %[1]s怎么恢复出厂设置？
%[7]s
## %[1]s的规格参数有哪些？
| 项目 | 参数 |
%[8]s## 哪些套餐配%[1]s？
%[9]s
`, d.nameZH, d.kindZH, d.extraZH, "", d.setupZH, ledTable(d.ledZH), resetZH, specZH.String(), d.withZH)
		_ = i
		nvPairNamed("devices/"+d.slug+"-user-guide", "devices/"+d.nameZH+"使用指南", en, zh)
	}

	nvPair("devices/router-models-compared",
		`# Router models compared
Applies to: R3, R5, R5 Pro
## Which router should I choose?
Choose the R3 for a small home, the R5 for a flat or a small house and the R5 Pro for a large home or a home office with many devices.
| Model | Wi-Fi | Coverage | Devices | Reset button hold |
| R3 | Wi-Fi 5 | 80 square meters | 64 | 5 seconds |
| R5 | Wi-Fi 6 | 120 square meters | 128 | 8 seconds |
| R5 Pro | Wi-Fi 6 tri band | 160 square meters | 256 | 10 seconds |
## Can I swap my R3 for an R5?
Yes. Customers on Fiber 500 can buy an R5 as an upgrade and return the R3. The upgrade price is shown in the NovaNet app.
## Is there a router for Fiber 1000?
Fiber 1000 comes with the R5 Pro, because it is the only router with a 2.5G port that can carry the full speed.
`,
		`# 路由器型号对比
适用型号：R3、R5、R5 Pro
## 该选哪款路由器？
小户型选R3，公寓或小别墅选R5，大户型或在家办公设备多的选R5 Pro。
| 型号 | 无线 | 覆盖面积 | 连接设备数 | Reset按住时间 |
| R3 | Wi-Fi 5 | 80平方米 | 64台 | 5秒 |
| R5 | Wi-Fi 6 | 120平方米 | 128台 | 8秒 |
| R5 Pro | Wi-Fi 6三频 | 160平方米 | 256台 | 10秒 |
## R3可以换成R5吗？
可以。光纤500客户可以购买R5升级并归还R3，升级价格在星联宽带应用里查看。
## 光纤1000配什么路由器？
光纤1000配R5 Pro，因为它是唯一带2.5G网口、能跑满速率的型号。
`)

	nvPair("devices/tv-box-models-compared",
		`# TV box models compared
## T2 or T2 Max: what is different?
The T2 plays 4K at 30 frames per second and has 16 GB of storage. The T2 Max plays 4K HDR at 60 frames per second and has 64 GB of storage.
## Which TV box is included in my plan?
Fiber 1000 includes one T2. No plan includes the T2 Max.
## How much is a T2 Max?
The T2 Max is sold in service halls and in the NovaNet app. The price is shown at checkout.
`,
		`# 电视盒型号对比
## T2和T2 Max有什么区别？
T2支持4K、每秒30帧，存储16GB；T2 Max支持4K HDR、每秒60帧，存储64GB。
## 我的套餐含哪款电视盒？
光纤1000含一台T2，没有套餐含T2 Max。
## T2 Max多少钱？
T2 Max在营业厅和星联宽带应用里销售，价格以结算页面为准。
`)

	nvPair("devices/firmware-updates",
		`# Firmware updates
## How do I update my router?
Routers update by themselves at night between 2:00 and 4:00 when new firmware is available. You do not need to do anything.
## Can I update the router by hand?
Yes. Open the NovaNet app, choose your router, then Check for updates. The router restarts for about 3 minutes.
## Will an update change my Wi-Fi settings?
No. Your Wi-Fi name, password and settings are kept.
`,
		`# 固件升级
## 路由器怎么升级？
有新固件时，路由器会在夜里2:00到4:00自动升级，无需任何操作。
## 可以手动升级吗？
可以。打开星联宽带应用，选择您的路由器，点“检查更新”，路由器会重启约3分钟。
## 升级会改变我的Wi-Fi设置吗？
不会，您的Wi-Fi名称、密码和设置都会保留。
`)
}
