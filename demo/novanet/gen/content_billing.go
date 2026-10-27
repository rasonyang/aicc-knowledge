// SPDX-License-Identifier: Apache-2.0

package main

// Billing, payment and top-up documents.

func init() {
	nvPair("billing/how-to-read-your-bill",
		`# How to read your bill
Applies to: mobile and broadband accounts
## When is my bill issued?
Your bill is issued on your bill date. The default bill date is the 1st of the month, and the bill covers the previous calendar month.
## What is on my bill?
The bill lists the monthly plan fee, add-ons, one-off charges, roaming, credits and any late fee. Each line has a short code that you can tap in the app to see the detail.
| Line | What it means |
| PLAN | Monthly fee of your plan |
| ADDON | Booster packs and TV box rental |
| ONEOFF | Installation, SIM replacement and number change fees |
| ROAM | Roaming packs bought in the month |
| CREDIT | Refunds, outage credits and promotions |
| LATE | Late fee for an overdue bill |
## Why is my bill higher this month?
The usual reasons are a Booster or roaming pack bought during the month, a one-off fee such as installation, a plan change that was prorated, the end of a promotion discount, or a late fee from an overdue bill. Open the bill in the app and check the ADDON, ONEOFF and ROAM lines first.
## Why is my first bill odd?
The first bill covers a part month. The plan fee is prorated by day from the activation date, and the one-off fees are added.
## Where can I see my usage?
Open Usage in the NovaNet app. It shows data, voice minutes and roaming used since your bill date.
`,
		`# 账单怎么看
适用范围：手机和宽带账户
## 账单什么时候出？
账单在账单日出具。默认账单日为每月1日，账单涵盖上一个自然月。
## 账单上有哪些内容？
账单列出套餐月费、增值业务、一次性费用、漫游、返还和滞纳金，每一行都有简码，在应用里点一下可以查看明细。
| 项目 | 含义 |
| PLAN | 套餐月费 |
| ADDON | 加油包和电视盒租赁费 |
| ONEOFF | 安装费、补卡费和换号费 |
| ROAM | 当月购买的漫游包 |
| CREDIT | 退款、故障返还和优惠 |
| LATE | 逾期账单的滞纳金 |
## 这个月账单为什么比平时高？
常见原因有：当月购买了加油包或漫游包、产生了安装费等一次性费用、中途换套餐按天折算、优惠到期，或者上期账单逾期产生了滞纳金。先在应用里看账单的ADDON、ONEOFF和ROAM几行。
## 第一期账单为什么金额不整？
第一期账单只覆盖不满一个月的时间，套餐月费从开通日起按天折算，并加上一次性费用。
## 在哪里看已用流量和分钟数？
在星联宽带应用的“用量”页面，可以看到账单日以来已用的流量、通话分钟和漫游用量。
`)

	nvPair("billing/change-billing-date",
		`# Changing your bill date
## Can I change my bill date?
Yes. You can move your bill date to the 1st, the 10th or the 20th of the month in the NovaNet app under Billing.
## How often can I change it?
You can change the bill date once every 6 months.
## What happens to my next bill?
The next bill is prorated. It covers the days from your old bill date to the new one, so it can be shorter or longer than a month.
## Does the due date move too?
Yes. The due date is always 20 days after the bill date.
`,
		`# 修改账单日
## 可以修改账单日吗？
可以。在星联宽带应用的“账单”页面，可以把账单日改为每月1日、10日或20日。
## 多久可以改一次？
每6个月可以修改一次账单日。
## 改了账单日，下一期账单怎么算？
下一期账单按天折算，涵盖从旧账单日到新账单日的天数，所以可能比一个月短或长。
## 到期日也会变吗？
会。到期日总是账单日之后第20天。
`)

	nvPair("billing/e-invoice",
		`# Electronic invoices
## How do I get an invoice?
After you pay, request an electronic invoice in the NovaNet app under Billing, then Invoices. The invoice is sent to your email within 3 business days.
## Can I get an invoice in my company name?
Yes. Enter the company name and tax number when you request it. You cannot change them after the invoice is issued.
## Can I get invoices for earlier months?
You can request invoices for the last 12 months.
`,
		`# 电子发票
## 怎么开发票？
缴费后，在星联宽带应用的“账单”里选择“发票”申请电子发票，3个工作日内发送到您的邮箱。
## 可以开公司抬头的发票吗？
可以。申请时填写公司名称和税号，发票开出后不能修改。
## 能补开以前月份的发票吗？
可以补开最近12个月的发票。
`)

	nvPair("billing/auto-pay",
		`# Automatic payment
## How do I set up automatic payment?
Open Billing in the NovaNet app, choose Auto-pay and link a bank card. The bill is paid on its due date.
## Can I use more than one card?
You can link two cards. If the first payment fails, we try the second one the next day.
## How do I stop automatic payment?
Switch off Auto-pay in the app at least 2 days before the due date. A payment already taken cannot be cancelled.
## What if my card expires?
We message you 10 days before the card expires. Please link a new card, or the bill will have to be paid by hand.
`,
		`# 自动缴费
## 怎么开通自动缴费？
在星联宽带应用打开“账单”，选择“自动缴费”并绑定银行卡，账单将在到期日自动扣款。
## 可以绑定多张卡吗？
可以绑定两张卡。第一张卡扣款失败，次日会尝试第二张卡。
## 怎么关闭自动缴费？
最迟在到期日前2天，在应用里关闭自动缴费。已经扣掉的款项不能撤销。
## 银行卡过期了怎么办？
银行卡过期前10天我们会发短信提醒，请及时绑定新卡，否则账单需要手动缴费。
`)

	nvPair("billing/bill-dispute",
		`# Disputing a charge
## How do I dispute a charge on my bill?
Tap the charge in the NovaNet app and choose Dispute, or tell customer care. Please give the bill month and the line code.
## How long do I have to dispute a bill?
You have 60 days from the bill date to dispute a charge.
## How long does the review take?
We review a dispute within 7 business days. If we agree, the amount is credited on your next bill.
## Do I pay while the dispute is open?
Please pay the undisputed part by the due date. The disputed amount will not get a late fee while the review is open.
`,
		`# 账单争议
## 对账单上的费用有异议怎么办？
在星联宽带应用里点开这笔费用选择“争议”，或者告诉客服。请说明账单月份和费用代码。
## 多长时间内可以提出争议？
自账单日起60天内可以提出争议。
## 复核要多久？
我们在7个工作日内复核。认定属实的，金额会在下一期账单里返还。
## 争议期间还需要缴费吗？
没有争议的部分请按期缴纳。争议金额在复核期间不产生滞纳金。
`)

	nvPair("billing/mid-month-plan-change",
		`# Plan changes in the middle of a month
## What happens to my bill mid-month?
The old plan is charged until the day before the change and the new plan from the day of the change. Both are prorated by day.
## When does an upgrade take effect?
An upgrade takes effect immediately. A new contract term starts from the day of the upgrade.
## When does a downgrade take effect?
A downgrade takes effect on the next bill date.
## Is there a fee for changing plan?
Changing plan is free. Early termination fees apply only if you cancel, not when you upgrade.
`,
		`# 月中变更套餐
## 月中换套餐，账单怎么算？
旧套餐计费到变更前一天，新套餐从变更当天起计费，两段都按天折算。
## 升档什么时候生效？
升档立即生效，新的合约期从升档当天起重新计算。
## 降档什么时候生效？
降档从下一个账单日起生效。
## 换套餐收手续费吗？
换套餐不收手续费。只有取消套餐才会收提前解约费，升档不收。
`)

	nvPair("billing/balance-alerts-and-top-up",
		`# Balance alerts and top-up
## How do I top up my balance?
Top up in the NovaNet app, at a service hall or with a top-up card. The amounts are 30, 50 or 100 yuan.
## Do I get a bonus when I top up?
Larger top-ups come with a bonus. The bonus depends on the amount, and the current table is kept by the billing team.
## How do I get a low-balance alert?
Switch on Balance alert in the app. You get a text message when your balance falls below 10 yuan.
## Does the top-up balance expire?
Balance stays valid as long as the account is active. It is refunded to you if you close the account.
`,
		`# 余额提醒与充值
## 怎么充值？
可以在星联宽带应用、营业厅或用充值卡充值，面额为30元、50元或100元。
## 充值有赠送吗？
金额较大的充值有赠送，赠送内容因面额而异，最新的赠送表由计费团队维护。
## 怎么开通余额不足提醒？
在应用里打开“余额提醒”，余额低于10元时会收到短信。
## 充值余额会过期吗？
只要账户有效，余额就一直有效；销户时余额会退还给您。
`)

	nvPair("billing/payment-problems",
		`# Payment problems
## My payment failed. What should I do?
Check the card limit and the balance first. A failed payment is retried once on the next day, and you can always pay again in the app.
## Charged twice. How do I get a refund?
A duplicate payment is refunded to the original payment method within 7 business days after you report it.
## Can I pay somebody else's bill?
Yes. Enter their phone number or account number in the app. You do not need their password.
## Paid, but the bill shows unpaid?
It can take up to 30 minutes to update. If it still shows unpaid after one hour, contact customer care with the payment receipt.
`,
		`# 缴费常见问题
## 缴费失败怎么办？
先检查银行卡限额和余额。失败的缴费次日会重试一次，您也可以随时在应用里重新缴费。
## 被重复扣款了怎么退？
重复扣款在您报告后7个工作日内原路退回。
## 可以帮别人缴费吗？
可以。在应用里输入对方的手机号或账户号即可，不需要对方的密码。
## 钱扣了账单还显示未缴？
更新最多需要30分钟。超过一小时仍显示未缴，请带上缴费凭证联系客服。
`)

	nvPair("billing/payment-methods",
		`# Payment methods
## Which payment methods can I use?
You can pay in the NovaNet app with a bank card or a mobile wallet, in cash or by card at a service hall, at partner counters, or with a top-up card.
## Can I pay in cash?
Yes, at a service hall. Cash payments are not taken by the technician at your home.
## Is there a fee to pay by card?
No. We do not charge a fee for any payment method.
## What is the largest single payment?
A single payment in the app can be up to 5000 yuan.
`,
		`# 缴费方式
## 可以用哪些方式缴费？
可以在星联宽带应用里用银行卡或手机钱包缴费，也可以到营业厅用现金或刷卡缴费，或到合作柜台缴费，还可以使用充值卡。
## 可以交现金吗？
可以，到营业厅交现金。上门师傅不收现金。
## 用银行卡或钱包缴费要手续费吗？
不收。任何缴费方式都没有手续费。
## 一次最多可以缴多少钱？
应用里单笔最多可缴5000元。
`)

	nvPair("billing/extra-sim-billing",
		`# Billing for extra SIMs
## Who pays for an extra SIM?
The account holder pays. Charges of the extra SIM appear on the same bill under the extra SIM number.
## Can the extra SIM user see the bill?
No. Only the account holder can see the bill. The extra SIM user sees only their own usage.
## Is there a data limit per extra SIM?
All SIMs on the account share the data of the main plan. The account holder can set a monthly data limit for each extra SIM.
`,
		`# 副卡计费说明
## 副卡的费用谁来付？
由账户持有人支付，副卡产生的费用在同一份账单里按副卡号码列出。
## 副卡使用人能看到账单吗？
不能。只有账户持有人能看账单，副卡使用人只能看到自己的用量。
## 每张副卡有流量上限吗？
账户下所有卡共享主套餐流量，账户持有人可以为每张副卡设置每月流量上限。
`)

	nvPair("billing/credits-and-promotions-on-bill",
		`# Credits and discounts on the bill
## Where do I see my promotion discount?
A promotion discount appears as a CREDIT line on each bill while the promotion lasts.
## Why did my discount disappear?
The promotion has probably ended. The end date is shown in the app next to the promotion.
## When does a refund appear on the bill?
A refund to the account appears as a CREDIT line on the next bill after it is approved.
`,
		`# 账单中的返还与优惠
## 在哪里看优惠减免？
优惠期间，每期账单都有一行CREDIT显示优惠减免。
## 为什么优惠没有了？
可能是优惠已经到期，应用里优惠旁边会显示结束日期。
## 退款什么时候体现在账单里？
退款审核通过后，在下一期账单里以CREDIT一行体现。
`)
}
