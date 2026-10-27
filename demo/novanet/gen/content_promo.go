// SPDX-License-Identifier: Apache-2.0

package main

import "fmt"

// Promotions. Each runs for a fixed period that is written in the document;
// several have already ended, as old promotion pages do on a real file server.

type promo struct {
	slug, zhName     string
	titleEN, titleZH string
	whatEN, whatZH   string
	whoEN, whoZH     string
	periodEN         string
	periodZH         string
	howEN, howZH     string
	noteEN, noteZH   string
}

var promos = []promo{
	{
		slug: "double-11-data-festival", zhName: "双十一流量节",
		titleEN: "Double 11 data festival", titleZH: "双十一流量节",
		whatEN: "Booster 30 is sold for 29 yuan instead of 35 yuan. It gives 30 GB of extra data valid for 30 days.", whatZH: "加油包30特价29元，原价35元，提供30GB额外流量，有效期30天。",
		whoEN: "All mobile customers can buy it. Each account can buy up to 3 packs.", whoZH: "所有手机客户都可以购买，每个账户最多买3个。",
		periodEN: "The festival runs from 1 November 2026 to 11 November 2026.", periodZH: "活动时间为2026年11月1日至2026年11月11日。",
		howEN: "Open Add-ons in the NovaNet app and choose Booster 30.", howZH: "在星联宽带应用的“增值业务”里选择加油包30。",
		noteEN: "The festival price cannot be combined with other discounts.", noteZH: "活动价不能与其他优惠叠加。",
	},
	{
		slug: "summer-travel-roaming-deal", zhName: "暑期出行漫游特惠",
		titleEN: "Summer travel roaming deal", titleZH: "暑期出行漫游特惠",
		whatEN: "The Asia 7 Day roaming pack was sold for 88 yuan instead of the normal price.", whatZH: "亚洲7日漫游包特价88元，低于正常价格。",
		whoEN: "All mobile customers could buy it.", whoZH: "所有手机客户均可购买。",
		periodEN: "The deal ran from 15 June 2026 to 31 August 2026 and has ended.", periodZH: "活动时间为2026年6月15日至2026年8月31日，现已结束。",
		howEN: "The pack was bought in the NovaNet app under Roaming.", howZH: "在星联宽带应用的“漫游”页面购买。",
		noteEN: "The deal could not be combined with the Plan 59 Plus roaming discount.", noteZH: "该优惠不能与59元Plus套餐的漫游折扣叠加。",
	},
	{
		slug: "refer-a-friend", zhName: "老带新推荐有礼",
		titleEN: "Refer a friend", titleZH: "老带新推荐有礼",
		whatEN: "When a friend signs up with your referral code, you get a 20 yuan bill credit and your friend gets 10 GB of bonus data.", whatZH: "朋友用您的推荐码办理业务，您获得20元账单返还，朋友获得10GB赠送流量。",
		whoEN: "Existing customers whose account is at least 3 months old can refer new customers.", whoZH: "入网满3个月的老客户可以推荐新客户。",
		periodEN: "The offer runs from 1 March 2026 to 31 December 2026.", periodZH: "活动时间为2026年3月1日至2026年12月31日。",
		howEN: "Find your referral code in the NovaNet app under Invite friends and share it.", howZH: "在星联宽带应用的“邀请好友”里找到推荐码并分享给朋友。",
		noteEN: "You can earn credit for up to 5 friends.", noteZH: "最多可为5位朋友获得返还。",
	},
	{
		slug: "student-data-bonus", zhName: "学生流量福利",
		titleEN: "Student data bonus", titleZH: "学生流量福利",
		whatEN: "Students on Plan 59 get 10 GB of extra data every month while the offer runs.", whatZH: "59元套餐的学生用户在活动期间每月额外获得10GB流量。",
		whoEN: "Students with a valid student ID, aged 16 or older.", whoZH: "持有效学生证、年满16周岁的学生。",
		periodEN: "The offer runs from 1 September 2026 to 31 October 2026.", periodZH: "活动时间为2026年9月1日至2026年10月31日。",
		howEN: "Upload a photo of your student ID in the NovaNet app under Offers.", howZH: "在星联宽带应用的“优惠”里上传学生证照片。",
		noteEN: "The bonus data expires at the end of each month.", noteZH: "赠送流量当月月底清零。",
	},
	{
		slug: "fiber-1000-upgrade-offer", zhName: "光纤1000升级优惠",
		titleEN: "Fiber 1000 upgrade offer", titleZH: "光纤1000升级优惠",
		whatEN: "Upgrading from Fiber 500 to Fiber 1000 normally has a one-off upgrade fee of 50 yuan. During the offer the fee is waived.", whatZH: "光纤500升级到光纤1000通常收取一次性升级费50元，活动期间免收。",
		whoEN: "Existing Fiber 500 customers in a district where Fiber 1000 is available.", whoZH: "所在区域已开通光纤1000的光纤500老客户。",
		periodEN: "The offer runs from 1 October 2026 to 31 December 2026.", periodZH: "活动时间为2026年10月1日至2026年12月31日。",
		howEN: "Request the upgrade in the NovaNet app under My broadband.", howZH: "在星联宽带应用的“我的宽带”里申请升级。",
		noteEN: "A new 24-month contract starts on the day of the upgrade.", noteZH: "升级当天起重新计算24个月合约期。",
	},
	{
		slug: "autopay-discount", zhName: "自动缴费立减",
		titleEN: "Auto-pay discount", titleZH: "自动缴费立减",
		whatEN: "Customers who pay by auto-pay get 5 yuan off their bill every month for 6 months.", whatZH: "开通自动缴费的客户，连续6个月每月账单立减5元。",
		whoEN: "Customers who switch on auto-pay during the offer. Customers who already use auto-pay are not included.", whoZH: "活动期间开通自动缴费的客户，已经在使用自动缴费的客户不参加。",
		periodEN: "The offer runs from 1 July 2026 to 31 December 2026.", periodZH: "活动时间为2026年7月1日至2026年12月31日。",
		howEN: "Switch on Auto-pay in the app under Billing.", howZH: "在应用的“账单”里开通自动缴费。",
		noteEN: "The discount ends if auto-pay is switched off.", noteZH: "关闭自动缴费后，优惠即时终止。",
	},
	{
		slug: "router-trade-in", zhName: "路由器以旧换新",
		titleEN: "Router trade-in", titleZH: "路由器以旧换新",
		whatEN: "Swap your R3 for an R5 for 99 yuan instead of the normal 199 yuan.", whatZH: "用R3换购R5，只需99元，原价199元。",
		whoEN: "Fiber 500 customers who return a working R3.", whoZH: "归还完好R3的光纤500客户。",
		periodEN: "The offer runs from 1 September 2026 to 30 November 2026.", periodZH: "活动时间为2026年9月1日至2026年11月30日。",
		howEN: "Book the swap in the NovaNet app under Devices. The technician brings the R5 and takes the R3.", howZH: "在应用的“设备”里预约换购，师傅会带来R5并取走R3。",
		noteEN: "The offer applies to one swap per account.", noteZH: "每个账户限换购一次。",
	},
	{
		slug: "esim-switch-bonus", zhName: "eSIM转换礼",
		titleEN: "eSIM switch bonus", titleZH: "eSIM转换礼",
		whatEN: "Move your number to an eSIM and get 2 GB of bonus data.", whatZH: "把号码转到eSIM上，可获赠2GB流量。",
		whoEN: "Customers on Plan 59, Plan 59 Plus and Plan 99.", whoZH: "59元套餐、59元Plus套餐和99元套餐的客户。",
		periodEN: "The offer runs from 1 August 2026 to 31 December 2026.", periodZH: "活动时间为2026年8月1日至2026年12月31日。",
		howEN: "Request the eSIM transfer in the NovaNet app. The bonus arrives within 24 hours of activation.", howZH: "在应用里申请eSIM转换，开通后24小时内到账。",
		noteEN: "The bonus data is valid for 30 days.", noteZH: "赠送流量有效期30天。",
	},
	{
		slug: "weekend-tv-pass", zhName: "周末电视体验",
		titleEN: "TV box rental holiday", titleZH: "电视盒租赁免费期",
		whatEN: "The monthly rental of the T2 TV box is free for 3 months.", whatZH: "T2电视盒的月租费免收3个月。",
		whoEN: "Fiber 500 customers who add a T2 during the offer.", whoZH: "活动期间加装T2的光纤500客户。",
		periodEN: "The offer runs from 1 September 2026 to 31 December 2026.", periodZH: "活动时间为2026年9月1日至2026年12月31日。",
		howEN: "Add the TV box in the app under My broadband.", howZH: "在应用的“我的宽带”里加装电视盒。",
		noteEN: "After 3 months the normal 10 yuan per month applies.", noteZH: "3个月后恢复每月10元的正常月租。",
	},
	{
		slug: "new-year-data-gift", zhName: "新年流量礼",
		titleEN: "New Year data gift", titleZH: "新年流量礼",
		whatEN: "Every mobile customer gets 5 GB of free data to celebrate the new year.", whatZH: "新年到来，每位手机客户获赠5GB免费流量。",
		whoEN: "All mobile customers with an active line.", whoZH: "所有在用手机号码的客户。",
		periodEN: "The gift is credited between 1 January 2027 and 15 January 2027.", periodZH: "流量将在2027年1月1日至2027年1月15日期间到账。",
		howEN: "No action is needed. The data appears in the app.", howZH: "无需操作，流量会出现在应用里。",
		noteEN: "The gift data is valid until 31 January 2027.", noteZH: "赠送流量有效期至2027年1月31日。",
	},
	{
		slug: "mid-autumn-family-gift-2025", zhName: "中秋家庭礼包_2025",
		titleEN: "Mid-Autumn family gift 2025", titleZH: "2025中秋家庭礼包",
		whatEN: "Customers with two or more lines received 5 GB of shared family data.", whatZH: "拥有两个及以上号码的客户获赠5GB家庭共享流量。",
		whoEN: "Customers with a main SIM and at least one extra SIM.", whoZH: "拥有主卡和至少一张副卡的客户。",
		periodEN: "The gift ran from 1 September 2025 to 15 October 2025 and has ended.", periodZH: "活动时间为2025年9月1日至2025年10月15日，现已结束。",
		howEN: "The data was credited automatically.", howZH: "流量自动到账。",
		noteEN: "This promotion is closed.", noteZH: "该活动已关闭。",
	},
	{
		slug: "senior-care-welcome", zhName: "敬老套餐开通礼",
		titleEN: "Senior Care welcome gift", titleZH: "敬老套餐开通礼",
		whatEN: "New Senior Care customers get a free large-print guide and one free visit from a staff member who sets up the phone.", whatZH: "新办敬老套餐的客户免费获得大字版使用手册，并可预约一次上门协助设置手机。",
		whoEN: "Customers aged 65 or older who sign up for Senior Care.", whoZH: "年满65周岁办理敬老套餐的客户。",
		periodEN: "The gift is available from 1 April 2026 to 31 March 2027.", periodZH: "领取时间为2026年4月1日至2027年3月31日。",
		howEN: "Ask for it when you sign up at a service hall.", howZH: "在营业厅办理时提出即可。",
		noteEN: "The home visit is available in districts that have a service hall.", noteZH: "上门协助仅限设有营业厅的区域。",
	},
	{
		slug: "fiber-500-new-customer-gift", zhName: "光纤500新装礼",
		titleEN: "Fiber 500 new installation gift", titleZH: "光纤500新装礼",
		whatEN: "New Fiber 500 customers who order in the NovaNet app get the 99 yuan installation fee waived and a free Wi-Fi health check.", whatZH: "通过星联宽带应用下单的光纤500新客户，免收99元安装费，并赠送一次Wi-Fi体检。",
		whoEN: "New customers at an address inside a service area.", whoZH: "服务区域内地址的新客户。",
		periodEN: "The gift runs from 1 January 2026 to 31 December 2026.", periodZH: "活动时间为2026年1月1日至2026年12月31日。",
		howEN: "Order Fiber 500 in the app and choose the installation time.", howZH: "在应用里订购光纤500并选择安装时间。",
		noteEN: "Orders made at a service hall pay the normal installation fee.", noteZH: "在营业厅下单的，按正常标准收取安装费。",
	},
}

func init() {
	for i, p := range promos {
		var en, zh string
		if i%2 == 0 {
			en = fmt.Sprintf(`# %[1]s
Owner: Marketing
## What is the offer?
%[2]s
## Who can join?
%[3]s
## When does it run?
%[4]s
## How do I join?
%[5]s
## Are there other terms?
%[6]s
`, p.titleEN, p.whatEN, p.whoEN, p.periodEN, p.howEN, p.noteEN)
			zh = fmt.Sprintf(`# %[1]s
负责部门：市场部
## 活动内容是什么？
%[2]s
## 谁可以参加？
%[3]s
## 活动什么时候进行？
%[4]s
## 怎么参加？
%[5]s
## 还有其他条款吗？
%[6]s
`, p.titleZH, p.whatZH, p.whoZH, p.periodZH, p.howZH, p.noteZH)
		} else {
			en = fmt.Sprintf(`# %[1]s
## Offer
%[2]s %[3]s
## Eligibility
%[4]s
## How to join
%[5]s
## Terms
%[6]s
`, p.titleEN, p.whatEN, p.periodEN, p.whoEN, p.howEN, p.noteEN)
			zh = fmt.Sprintf(`# %[1]s
## 优惠内容
%[2]s%[3]s
## 参加条件
%[4]s
## 参加方式
%[5]s
## 条款
%[6]s
`, p.titleZH, p.whatZH, p.periodZH, p.whoZH, p.howZH, p.noteZH)
		}
		nvPairNamed("promotions/"+p.slug, "promotions/"+p.zhName, en, zh)
	}

	nvPair("addons/booster-data-packs",
		`# Booster data packs
Applies to: mobile plans
## What is a Booster pack?
A Booster pack adds data to your plan when you run out. Booster 10 gives 10 GB for 15 yuan and Booster 30 gives 30 GB for 35 yuan. Both are valid for 30 days.
## How do I buy a Booster pack?
Open Add-ons in the NovaNet app and choose Booster 10 or Booster 30. The pack is added to your next bill.
## Is full speed back after a pack?
Yes. When your plan data is used up, Booster data is used at full speed. You do not need to restart the phone.
## Which data is used first?
Rolled-over data is used first, then the plan data, then Booster data.
`,
		`# 加油包流量
适用范围：手机套餐
## 什么是加油包？
流量不够用时可以购买加油包补充。加油包10为10GB，15元；加油包30为30GB，35元，有效期均为30天。
## 怎么购买加油包？
在星联宽带应用的“增值业务”里选择加油包10或加油包30，费用计入下期账单。
## 买了加油包就能恢复全速吗？
可以。套餐流量用完后会使用加油包流量，全速上网，无需重启手机。
## 流量的使用顺序是什么？
先用结转流量，再用套餐流量，最后用加油包流量。
`)

	nvPair("roaming/how-roaming-works",
		`# How roaming works
## How do I use my phone abroad?
Data roaming is off by default. Buy a roaming pack in the NovaNet app and switch on data roaming on the phone.
## When does a roaming pack start?
A pack starts on the date you choose, or at the first use abroad if you choose automatic start. Its days are counted in 24-hour blocks.
## What happens when the pack ends?
Data stops. We never charge data roaming outside a pack, so you will not get a surprise bill.
## Can I buy a pack while abroad?
Yes, if you have a working connection. The app and a text message from NovaNet are both free to use abroad.
`,
		`# 漫游使用说明
## 出国怎么使用手机？
数据漫游默认关闭。请在星联宽带应用里购买漫游包，并在手机里打开数据漫游。
## 漫游包什么时候开始计时？
漫游包在您选定的日期生效，选择自动生效的，则在国外首次用网时生效。天数按24小时一个周期计算。
## 漫游包到期后会怎样？
到期后数据即停止。漫游包之外我们不会收取数据漫游费，不会产生意外账单。
## 已经在国外还能买漫游包吗？
可以，前提是手机有可用的网络。在国外访问应用和接收星联宽带的短信都是免费的。
`)

	nvPair("roaming/roaming-packs-and-prices",
		`# Roaming packs
## Which roaming packs are there?
NovaNet sells packs for Asia, Europe and the whole world. The table shows the packs we sell most.
| Pack | Region | Days | Price (yuan) |
| Asia 3 Day | Asia | 3 | 68 |
| Asia 7 Day | Asia | 7 | 108 |
| Global 15 Day | Worldwide | 15 | 288 |
## How much is the Europe pack?
The Europe 7 Day pack is available. Its price is shown in the NovaNet app before you buy.
## Which plans get a discount?
Plan 59 Plus customers get 10 percent off every pack. Plan 99 customers get one free Asia 3 Day pack each calendar year.
`,
		`# 漫游包
## 有哪些漫游包？
星联宽带销售亚洲、欧洲和全球漫游包，下表是销量最高的几款。
| 套餐包 | 地区 | 天数 | 价格（元） |
| 亚洲3日 | 亚洲 | 3 | 68 |
| 亚洲7日 | 亚洲 | 7 | 108 |
| 全球15日 | 全球 | 15 | 288 |
## 欧洲漫游包多少钱？
欧洲7日漫游包可以购买，价格在应用里购买前会显示。
## 哪些套餐有优惠？
59元Plus套餐用户购买任意漫游包享九折，99元套餐用户每个自然年赠送一个亚洲3日漫游包。
`)

	nvPair("roaming/calls-and-texts-abroad",
		`# Calls and texts abroad
## Do I pay to receive calls abroad?
Receiving calls while you have an active roaming pack is free. Without a pack, receiving calls costs 1 yuan per minute.
## What does a call from abroad cost?
Calls you make from abroad cost 2 yuan per minute. Texts cost 0.50 yuan each.
## Can I use Wi-Fi calling abroad?
Yes. Wi-Fi calling uses the data of the Wi-Fi network and costs nothing extra from NovaNet.
`,
		`# 国外通话和短信
## 在国外接听电话收费吗？
有生效的漫游包时，接听电话免费；没有漫游包时，接听每分钟1元。
## 在国外拨打电话多少钱？
在国外拨打电话每分钟2元，短信每条0.50元。
## 在国外可以用Wi-Fi通话吗？
可以。Wi-Fi通话使用Wi-Fi网络的流量，星联宽带不另外收费。
`)

	nvPair("roaming/roaming-pack-refunds",
		`# Roaming pack validity and refunds
## Can I get a roaming pack refunded?
A roaming pack that has not started can be refunded in full from the app. A pack that has started cannot be refunded.
## Can I change the start date?
Yes, up to 2 hours before the start time.
## What if I cut my trip short?
The pack stays active until its days run out. Unused days are not refunded.
`,
		`# 漫游包有效期与退款
## 漫游包可以退款吗？
尚未生效的漫游包可以在应用里全额退款，已经生效的漫游包不能退款。
## 可以修改生效日期吗？
可以，最晚在生效时间前2小时修改。
## 提前结束行程怎么办？
漫游包在天数用完前一直有效，未使用的天数不退款。
`)

	nvPair("roaming/travel-checklist",
		`# Travel checklist
## What should I do before I travel?
Buy a roaming pack, switch on data roaming, save the NovaNet app login offline, and check that your phone is unlocked. Do not forget to turn off auto-updates to save data.
`,
		`# 出行前清单
## 出国前应该做什么？
购买漫游包，打开数据漫游，确认应用登录信息，并确认手机没有被锁。别忘了关闭自动更新以节省流量。
`)

	// SIM and account services.
	nvPair("sim/activate-your-sim",
		`# Activating a new SIM
## How do I activate my new SIM?
Insert the SIM, switch the phone on and follow the steps in the NovaNet app. Activation takes about 10 minutes and needs the ID of the account holder.
## What if the SIM is not recognized?
Restart the phone. If it still says Invalid SIM, ask for a replacement at a service hall.
## Do I need to register the SIM?
Yes. The line works only after the ID has been checked.
`,
		`# 新SIM卡激活
## 新SIM卡怎么激活？
装入SIM卡，开机后按星联宽带应用中的步骤操作，激活约需10分钟，需要账户持有人的身份证件。
## 手机识别不了SIM卡怎么办？
重启手机；如果仍提示无效SIM卡，请到营业厅补卡。
## SIM卡需要实名登记吗？
需要。证件核验通过后号码才能使用。
`)

	nvPair("sim/pause-your-line",
		`# Pausing a line
## Can I pause my line while I am away?
Yes. A line can be paused for up to 90 days a year. A paused line costs 5 yuan per month and cannot make or receive calls.
## How do I pause or resume?
Use the NovaNet app under My line. Resuming takes effect within 1 hour.
## Does a pause extend my contract?
No. The contract keeps running while the line is paused.
`,
		`# 号码暂停
## 离开一段时间可以暂停号码吗？
可以。号码每年最多暂停90天，暂停期间每月收费5元，不能拨打和接听电话。
## 怎么暂停和恢复？
在星联宽带应用的“我的号码”里操作，恢复在1小时内生效。
## 暂停会延长合约吗？
不会，暂停期间合约照常计算。
`)

	nvPair("sim/transfer-account-ownership",
		`# Transferring an account to someone else
## Can I transfer my account?
Yes. Both people must visit a service hall with their IDs. The new holder takes over the plan, the contract and the balance.
## Is there a fee?
Transfer is free. Any unpaid bill must be paid before the transfer.
`,
		`# 过户
## 账户可以过户给别人吗？
可以。双方需要带身份证到营业厅办理，新持有人承接套餐、合约和余额。
## 过户收费吗？
免费。过户前须结清欠费。
`)

	nvPair("sim/change-your-number",
		`# Changing your phone number
## How much does a number change cost?
Changing your number costs 30 yuan. You can pick a new number at a service hall.
## Will my old number be kept?
The old number is held for 30 days and then released.
`,
		`# 更换手机号码
## 换号要多少钱？
换号费用30元，可以到营业厅挑选新号码。
## 旧号码会保留吗？
旧号码保留30天，之后释放。
`)

	nvPair("sim/cancel-service",
		`# Cancelling service
## How do I cancel my plan?
Tell us in the NovaNet app or at a service hall. Cancellation takes effect 30 days after the request, or on the contract end date if that comes later.
## Will I get my balance back?
Yes, the remaining top-up balance is paid back within 7 business days.
## What happens to my number?
The number is held for 30 days and then released. Port it out first if you want to keep it.
`,
		`# 取消服务
## 怎么取消套餐？
在星联宽带应用或营业厅提出申请。取消自申请日起30天后生效，如果合约到期日更晚，则在合约到期日生效。
## 余额会退还吗？
会，剩余充值余额在7个工作日内退还。
## 号码会怎么处理？
号码保留30天后释放。想保留号码，请先办理转出。
`)

	// The NovaNet app.
	nvPair("app/getting-started",
		`# Getting started with the NovaNet app
## How do I sign in the first time?
Download the NovaNet app, enter your phone number and the SMS code. Broadband-only customers sign in with their account number.
## What can I do in the app?
You can pay bills, buy add-ons, check usage, book repairs, change your Wi-Fi password and chat with customer care.
## Does the app cost data?
The NovaNet app does not use your mobile data allowance when you are on a NovaNet connection.
`,
		`# 星联宽带应用入门
## 第一次怎么登录应用？
下载星联宽带应用，输入手机号和短信验证码。只办宽带的客户用账户号码登录。
## 在应用里可以做什么？
可以缴费、购买增值业务、查看用量、预约报修、修改Wi-Fi密码，以及在线联系客服。
## 使用应用会消耗流量吗？
使用星联宽带网络时，访问应用不消耗您的手机流量。
`)

	nvPair("app/report-a-fault",
		`# Reporting a fault
## How do I report a fault?
Open Help in the NovaNet app and choose Report a fault. Describe the problem and pick a time for the technician.
## How fast will a technician come?
A repair visit is usually made within 2 days. Priority customers on Senior Care are visited the next day.
## Is the visit free?
The visit is free when the fault is on our side. If the fault is in your own equipment, a visit costs 80 yuan.
`,
		`# 故障报修
## 怎么报修？
打开星联宽带应用的“帮助”，选择“故障报修”，描述问题并选择上门时间。
## 多久能上门？
一般2天内上门维修，敬老套餐客户次日上门。
## 上门维修收费吗？
故障在我们这一侧的，上门免费；故障出在您自己的设备上的，上门收费80元。
`)

	nvPair("app/usage-alerts",
		`# Usage alerts
## How do I get a low-data alert?
Switch on Usage alerts in the app. You get a text message when you have used 80 percent and again at 100 percent of your data.
## Can I change the alert level?
Yes, you can choose 50, 80 or 90 percent.
`,
		`# 用量提醒
## 流量快用完时怎么收到提醒？
在应用里打开“用量提醒”，流量用到80%和100%时各收到一条短信。
## 可以改提醒比例吗？
可以，可选50%、80%或90%。
`)

	// Value-added services.
	nvPair("addons/value-added-services",
		`# Value-added services
## What is NovaCloud?
NovaCloud is online storage for photos and files. The first 5 GB are free and 100 GB cost 6 yuan per month.
## What is Caller ID?
Caller ID shows the number of the person calling you. It is free on every plan.
## What is voicemail?
Voicemail records messages when you cannot answer. It is free and keeps messages for 14 days.
## What is call screening?
Call screening blocks calls from numbers reported as spam. It is free and on by default.
`,
		`# 增值业务
## 星联云盘是什么？
星联云盘是存放照片和文件的在线存储空间，前5GB免费，100GB每月6元。
## 来电显示是什么？
来电显示可以显示来电号码，所有套餐免费。
## 语音信箱是什么？
无法接听时语音信箱可以为您录下留言，免费，留言保存14天。
## 骚扰拦截是什么？
骚扰拦截会拦截被举报为骚扰的号码来电，免费，默认开启。
`)

	nvPair("business/business-broadband",
		`# Business broadband
## Is there broadband for businesses?
Yes. Business broadband is quoted per site, with fixed IP addresses and a repair time commitment. Our enterprise sales team prepares the quote.
## Can I use a home plan for a shop?
Home plans are for personal use. A shop or office needs a business plan.
`,
		`# 企业宽带
## 星联宽带提供企业宽带吗？
提供。企业宽带按站点报价，含固定IP地址和维修时限承诺，由企业销售团队出具报价。
## 家庭套餐可以用在商铺里吗？
家庭套餐仅限个人使用，商铺或办公室需要办理企业套餐。
`)
}
