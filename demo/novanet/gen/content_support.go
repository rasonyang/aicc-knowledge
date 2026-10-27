// SPDX-License-Identifier: Apache-2.0

package main

// Troubleshooting articles for home broadband, TV and mobile.

func init() {
	nvPair("support/broadband/internet-light-is-red",
		`# The Internet light on my router is red
Applies to: R3, R5, R5 Pro
## What does a red Internet light mean?
A red Internet light means the router cannot reach the internet. Either the cable to the optical modem is loose, or the fiber line is down.
## What should I check first?
Check that the cable between the optical modem and the WAN port is firmly plugged in at both ends. Then check the LOS light on the optical modem.
## What if the LOS light blinks red?
A blinking red LOS light means the modem sees no light on the fiber. Do not bend or pull the fiber. Restart the modem once and, if the light stays red, book a repair in the NovaNet app.
## What if the modem lights are all green?
Restart the router by unplugging it for 30 seconds. If the Internet light is still red, reset the router and set it up again.
`,
		`# 路由器Internet灯是红色的
适用型号：R3、R5、R5 Pro
## Internet灯红色代表什么？
Internet灯红色表示路由器连不上互联网，可能是光猫到路由器的网线松了，也可能是光纤线路中断。
## 先检查什么？
先检查光猫和路由器WAN口之间的网线两端是否插紧，再看光猫上的LOS灯。
## LOS灯红灯闪烁怎么办？
LOS灯红灯闪烁表示光猫收不到光信号。不要弯折或拉扯光纤，先把光猫重启一次，灯仍是红色的话，请在星联宽带应用里预约报修。
## 光猫的灯都是绿色的怎么办？
把路由器电源拔掉30秒再插上。Internet灯仍是红色，就恢复出厂设置后重新设置。
`)

	nvPair("support/broadband/wifi-is-slow",
		`# Wi-Fi is slow
## Why is Wi-Fi slower than my plan?
Wi-Fi speed drops with distance, walls and the number of devices. A speed test over a network cable shows the real speed of the line.
## How do I get a faster Wi-Fi connection?
Move closer to the router, use the 5 GHz network for phones and laptops, and keep the router in the open, not in a cabinet.
## Does the number of devices matter?
Yes. Each router has a limit: 64 devices on the R3, 128 on the R5 and 256 on the R5 Pro. Slowdowns start well before the limit if many devices stream video at once.
## When should I add a mesh extender?
If some rooms have a weak signal, add an M1 mesh extender. It works with the R5 and the R5 Pro.
`,
		`# Wi-Fi很慢
## 为什么Wi-Fi比套餐速率慢很多？
Wi-Fi速率会随距离、墙壁和连接设备数下降。用网线直连做测速，才能看到线路的真实速率。
## 怎么让Wi-Fi更快？
离路由器近一点，手机和笔记本连5GHz网络，路由器放在空旷的地方，不要放进柜子里。
## 设备多会有影响吗？
会。每款路由器都有上限：R3为64台，R5为128台，R5 Pro为256台。很多设备同时看视频，远没到上限就会变慢。
## 什么时候需要加Mesh扩展器？
有的房间信号弱，就加装M1扩展器，它适配R5和R5 Pro。
`)

	nvPair("support/broadband/forgot-wifi-password",
		`# Forgot the Wi-Fi password
## Where is the default Wi-Fi password?
The default Wi-Fi name and password are printed on the label under the router.
## How do I change the Wi-Fi password?
Open the NovaNet app, choose your router and then Wi-Fi settings. The new password must have at least 8 characters.
## What if I changed it and forgot it?
Reset the router to factory settings. The default name and password on the label work again.
`,
		`# 忘记了Wi-Fi密码
## 默认的Wi-Fi密码在哪里？
默认的Wi-Fi名称和密码印在路由器底部的标签上。
## 怎么修改Wi-Fi密码？
打开星联宽带应用，选择您的路由器，进入Wi-Fi设置。新密码至少8位。
## 改了密码又忘了怎么办？
把路由器恢复出厂设置，标签上的默认名称和密码就可以再次使用。
`)

	nvPair("support/broadband/router-keeps-restarting",
		`# Router keeps restarting
## Why does my router restart by itself?
The most common causes are a loose or damaged power adapter, a power socket shared with a heavy appliance, and overheating.
## What can I do?
Use the original power adapter, plug the router into a wall socket of its own, and keep at least 10 centimeters of free space around it.
## When should I ask for a replacement?
If the router still restarts after these steps, book a repair in the NovaNet app. A router that is less than 24 months old is replaced free of charge.
`,
		`# 路由器自己反复重启
## 路由器为什么会自己重启？
常见原因有电源适配器松动或损坏、插座上还接着大功率电器，以及机器过热。
## 我可以怎么处理？
使用原装电源适配器，把路由器接到单独的墙壁插座上，四周留出至少10厘米的空间。
## 什么时候可以换新的？
按上面的方法处理后仍然重启，请在应用里预约报修。使用不满24个月的路由器免费更换。
`)

	nvPair("support/tv/tv-box-no-picture",
		`# TV box shows no picture
## What if the TV shows no picture?
Check that the TV is on the right HDMI input and that the HDMI cable is firmly plugged in at both ends.
## What if the status light is red?
A red status light means the box has no network. Check the Wi-Fi or the network cable.
## How do I restart the TV box?
Unplug the power for 10 seconds and plug it in again. It takes about 1 minute to start.
## What if the picture is blurry?
Check the network. A TV box on Wi-Fi far from the router can buffer. A network cable gives the best picture.
`,
		`# 电视盒没有画面
## 电视没有画面，先检查什么？
检查电视是否切换到了正确的HDMI输入，HDMI线两端是否插紧。
## 电视盒状态灯是红色的怎么办？
状态灯红色表示电视盒没有网络，请检查Wi-Fi或网线。
## 怎么重启电视盒？
拔掉电源10秒后重新插上，大约1分钟启动完成。
## 画面模糊或有马赛克怎么办？
检查网络。离路由器远的电视盒用Wi-Fi容易缓冲，接网线的画面最稳定。
`)

	nvPair("support/mobile/no-mobile-signal",
		`# No mobile signal
## What if my phone shows no signal?
Switch on airplane mode for 10 seconds and switch it off again. Then restart the phone.
## What if the signal is still missing?
Take the SIM out, wipe it with a dry cloth and put it back. Try the SIM in another phone to see whether the SIM or the phone is the problem.
## Could it be an outage in my area?
Check Network status in the NovaNet app. It lists known outages and planned maintenance by district.
## When should I replace my SIM?
If the SIM does not work in another phone either, ask for a replacement. It is free within 12 months of activation.
`,
		`# 手机没有信号
## 手机显示无信号怎么办？
先开启飞行模式10秒再关闭，然后重启手机。
## 信号还是没有怎么办？
把SIM卡取出，用干布擦拭后重新装好。也可以把SIM卡放进另一部手机，判断是卡还是手机的问题。
## 会不会是所在区域出了故障？
在星联宽带应用的“网络状态”里可以看到各区域已知的故障和计划内的维护。
## 什么时候需要换SIM卡？
SIM卡在别的手机里也不能用，就申请补卡。开通后12个月内免费。
`)

	nvPair("support/mobile/cannot-make-calls",
		`# Cannot make or receive calls
## Why can I not make calls?
First check whether the account is suspended for an overdue bill. A suspended line can still receive calls but cannot call out.
## Why can I not receive calls?
Check that call forwarding and Do Not Disturb are off, and that the number is not on your block list.
## Do calls over 5G need a setting?
Voice over 5G is switched on in the phone settings under Mobile network. Phones that do not support it fall back to 4G for calls.
`,
		`# 不能打电话或接电话
## 为什么打不出电话？
先看账户是否因欠费停机。停机的号码仍可接听来电，但不能呼出。
## 为什么接不到电话？
检查呼叫转移和勿扰模式是否关闭，以及号码是否在黑名单里。
## 5G网络下通话需要设置吗？
在手机“移动网络”设置里开启5G语音通话。不支持的手机通话时会回落到4G。
`)

	nvPair("support/mobile/sms-code-not-arriving",
		`# SMS verification code does not arrive
## Why is my verification code late?
Codes usually arrive within 1 minute. Poor signal, a full inbox or a blocked sender can delay them.
## What can I try?
Check that the phone has signal, free some inbox space, and make sure the sender is not blocked. Then request a new code.
## How many codes can I request?
You can request up to 5 codes per hour. After that, wait one hour and try again.
`,
		`# 收不到短信验证码
## 验证码为什么迟迟没到？
验证码一般1分钟内到达。信号差、收件箱已满或发件号码被拦截都会造成延迟。
## 可以怎么处理？
确认手机有信号，清理短信空间，确认没有拦截发件号码，然后重新获取验证码。
## 一小时最多能获取几次？
每小时最多获取5次，超过后请等待一小时再试。
`)

	nvPair("support/mobile/mobile-data-not-working",
		`# Mobile data does not work
## Why does my mobile data not work?
Check that mobile data is on, that you have data left this month, and that the phone is not in airplane mode.
## What if my data ran out?
When the monthly data runs out, the speed drops to 1 Mbps. Buy a Booster pack in the NovaNet app to get full speed again.
## What are the APN settings?
The phone sets the APN by itself when a NovaNet SIM is inserted. If you changed it, reset the network settings of the phone.
`,
		`# 手机上不了网
## 手机为什么上不了网？
检查移动数据开关是否打开、本月流量是否还有剩余，以及手机是否处于飞行模式。
## 流量用完了怎么办？
当月流量用完后速率降到1Mbps，在星联宽带应用里购买加油包即可恢复全速。
## APN怎么设置？
插入星联宽带的SIM卡后，手机会自动设置APN。如果改过，请重置手机的网络设置。
`)

	nvPair("support/mobile/roaming-no-data-abroad",
		`# No data when abroad
## Why is there no data when I travel?
Data roaming is off by default. Switch it on in the NovaNet app and on the phone, and buy a roaming pack for your destination.
## Must I buy the pack before I travel?
It is best to buy it before you leave. A pack can start on a date you choose or at the first use abroad.
## What if the pack is on but no data?
Restart the phone, choose the network automatically, and make sure Data roaming is on in the phone settings.
`,
		`# 出国后上不了网
## 出国后为什么没有流量？
数据漫游默认是关闭的。请在星联宽带应用和手机里都打开，并购买目的地的漫游包。
## 需要出发前购买漫游包吗？
建议出发前购买。漫游包可以在您选定的日期生效，也可以在国外首次用网时生效。
## 漫游包已生效还是上不了网怎么办？
重启手机，选择自动选网，并确认手机里的数据漫游已打开。
`)

	nvPair("support/mobile/esim-activation-failed",
		`# eSIM activation failed
## Why does my eSIM not activate?
The most common reasons are a QR code older than 24 hours, no internet during activation, and a phone that does not support eSIM.
## What should I do?
Connect the phone to Wi-Fi, request a new QR code in the NovaNet app and scan it again.
## What if the QR code was already used?
A QR code works on one phone only. If the first activation failed halfway, request a new one. Each new code counts as one transfer.
`,
		`# eSIM开通失败
## eSIM为什么开通不了？
最常见的原因是二维码超过24小时、开通时手机没有网络，以及手机不支持eSIM。
## 应该怎么处理？
让手机连上Wi-Fi，在星联宽带应用里重新获取二维码并再次扫描。
## 二维码已经用过怎么办？
二维码只能在一台手机上使用。第一次开通中途失败的，请重新获取，每次新二维码算一次转移。
`)

	nvPair("support/app/cannot-log-in-to-the-app",
		`# Cannot log in to the NovaNet app
## Why can I not log in?
Check the phone number and the SMS code. If you use a password, three wrong tries lock the login for 30 minutes.
## How do I reset my password?
Tap Forgot password on the login page and enter the code sent to your registered number.
## What if I no longer have my old number?
Visit a service hall with your ID. We can change the registered number after checking it.
`,
		`# 星联宽带应用登录不了
## 为什么登录不了？
检查手机号和短信验证码。如果使用密码登录，连续输错三次会锁定登录30分钟。
## 怎么重置密码？
在登录页点“忘记密码”，输入发送到注册号码的验证码。
## 旧号码已经不用了怎么办？
带上身份证到营业厅，核验后可以更换注册号码。
`)

	nvPair("support/broadband/speed-test-lower-than-plan",
		`# Speed test shows less than my plan
## Why is my speed test below my plan?
The plan speed is the maximum on the line. Wi-Fi, old devices and busy hours can lower a result.
## How should I test the speed?
Connect a laptop to the router with a network cable, close other apps, and run the test three times.
## When is a low result a fault?
If a cable test is below 80 percent of the plan speed at three different times of day, book a line check in the NovaNet app.
`,
		`# 测速结果低于套餐速率
## 为什么测速低于套餐速率？
套餐速率是线路的最大值，Wi-Fi、老旧设备和用网高峰都会拉低测速结果。
## 应该怎么测速？
用网线把笔记本接到路由器，关闭其他应用，连续测三次。
## 什么情况算故障？
网线直连的测速结果在一天中三个不同时段都低于套餐速率的80%，请在应用里预约线路检测。
`)

	nvPair("support/broadband/dead-zones-at-home",
		`# Weak Wi-Fi in some rooms
## Why is the Wi-Fi weak in some rooms?
Thick walls, metal doors and floors block the signal. One router often cannot cover a large home.
## What are the options?
Move the router to the middle of the home, or add an M1 mesh extender if you have an R5 or an R5 Pro. With an R3 you can swap to an R5.
## How many M1 extenders can I add?
An R5 works with up to 2 M1 extenders and an R5 Pro with up to 4.
`,
		`# 有些房间Wi-Fi信号弱
## 为什么有的房间信号很弱？
厚墙、金属门和楼层会阻挡信号，一台路由器往往覆盖不了大户型。
## 有哪些办法？
把路由器移到家中间；如果用的是R5或R5 Pro，可以加装M1扩展器；使用R3的可以换成R5。
## 最多能加几个M1扩展器？
R5最多搭配2个M1，R5 Pro最多搭配4个。
`)

	nvPair("support/network/how-to-check-for-an-outage",
		`# Is there an outage in my area?
## How do I check for an outage?
Open Network status in the NovaNet app. It lists current outages and planned maintenance for every district.
## How will I know when service is back?
The app updates the status as soon as the fault is cleared. You can switch on a text message alert for your district.
## Will I be compensated?
Home broadband customers are compensated when an outage lasts more than 24 hours. See the outage compensation policy.
`,
		`# 我所在的区域有故障吗？
## 怎么查询故障？
在星联宽带应用打开“网络状态”，可以看到各区域当前的故障和计划内的维护。
## 怎么知道服务恢复了？
故障排除后应用会立即更新状态，也可以为所在区域开启短信提醒。
## 会有补偿吗？
家庭宽带故障超过24小时的会有补偿，详见服务中断补偿政策。
`)

	nvPair("support/mobile/calls-dropping",
		`# Calls drop or sound bad
## Why do my calls drop?
Calls drop where the signal is weak, such as lifts, basements and fast trains. Moving near a window can help.
## What can I do about poor call quality?
Switch on Wi-Fi calling if your phone supports it. Calls then go over your home Wi-Fi.
## How do I report a bad spot?
Report it in the NovaNet app under Network feedback, with the place and the time.
`,
		`# 通话掉线或声音不清
## 通话为什么会掉线？
电梯、地下室和高速列车上信号弱，容易掉线，靠近窗边会好一些。
## 通话质量差怎么办？
手机支持的话开启Wi-Fi通话，通话会走家里的Wi-Fi。
## 怎么反馈信号差的地点？
在应用的“网络反馈”里提交，注明地点和时间。
`)
}
