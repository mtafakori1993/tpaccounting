// متن‌های فارسی افزوده‌شده در بازبینی و بهبود تجربه کاربری (در fa.ts ادغام می‌شود).
import type { navExtraEn, auditExtraEn, budgetsExtraEn, checksExtraEn, errorsExtraEn, payrollExtraEn, uiEn, validationExtraEn } from './ux.en';

type Widen<T> = { [K in keyof T]: T[K] extends string ? string : Widen<T[K]> };

export const uiFa: Widen<typeof uiEn> = {
  loadFailed: 'دریافت اطلاعات با خطا مواجه شد.', reportFailed: 'دریافت گزارش با خطا مواجه شد.', moreFilters: 'فیلترهای بیشتر', clearFilters: 'پاک کردن فیلترها',
  columns: 'ستون‌ها', showColumns: 'نمایش ستون‌ها', pagination: 'صفحه‌بندی', rangeOf: '{from} تا {to} از {total}', updatedAt: 'به‌روزرسانی: {time}',
  generatedAt: 'تهیه‌شده در {time}', exportAll: 'خروجی شامل همه رکوردهای منطبق با فیلترهاست، نه فقط همین صفحه.', exporting: 'در حال آماده‌سازی فایل…',
  exportFailed: 'تهیه فایل خروجی با خطا مواجه شد.', nextStep: 'گام بعدی', allDone: 'همه مراحل انجام شده است.', backTo: 'بازگشت به {name}',
  viewDetails: 'مشاهده جزئیات', overview: 'نمای کلی', history: 'سابقه', auditTrail: 'سوابق ممیزی', noAudit: 'هنوز سابقه‌ای ثبت نشده است.',
  accountingImpact: 'اثر حسابداری', noEntries: 'هنوز سندی صادر نشده است.', reversedBy: 'برگشت‌خورده با سند {number}',
  required: 'الزامی', optional: 'اختیاری', saveChanges: 'ذخیره تغییرات', discard: 'انصراف', dataBasis: 'مبنای داده', method: 'روش',
  andMore: '{count} مورد دیگر', of: 'از', perPage: 'در هر صفحه', print: 'چاپ', downloadTemplate: 'دریافت فایل نمونه', upload: 'بارگذاری',
  chooseFile: 'انتخاب فایل', dropHint: 'CSV یا Excel (.xlsx)، حداکثر ۵ مگابایت', status: 'وضعیت', yes: 'بله', no: 'خیر', settings: 'تنظیمات',
  effectiveFrom: 'معتبر از', effectiveTo: 'معتبر تا', openEnded: 'بدون تاریخ پایان', inForce: 'جاری', notInForce: 'غیرجاری',
  notes: 'یادداشت', name: 'نام', create: 'ایجاد', edit: 'ویرایش', deleteConfirm: 'این مورد حذف شود؟ این کار قابل بازگشت نیست.',
};

export const checksExtraFa: Widen<typeof checksExtraEn> = {
  subtitle: 'چک‌های دریافتی و پرداختی، وضعیت و اثر حسابداری آن‌ها.', registerSubtitle: 'همه چک‌ها با وضعیت، سررسید و طرف حساب.',
  reportSubtitle: 'چک‌ها را فیلتر، بررسی و صادر کنید. خروجی همیشه شامل کل نتیجه فیلترشده است.',
  dashboardSubtitle: 'آنچه امروز سررسید، معوق یا در جریان است.', calendarSubtitle: 'چک‌های جاری به تفکیک تاریخ سررسید.',
  tabs: { all: 'همه', received: 'دریافتی', issued: 'پرداختی', due: 'سررسید (۳۰ روز)', overdue: 'معوق', returned: 'برگشتی', 'in-custody': 'در صندوق' },
  kpi: { total: 'کل چک‌ها', amount: 'مبلغ کل', dueSoon: 'سررسید ۷ روز آینده', overdue: 'معوق', returned: 'برگشتی', live: 'در جریان' },
  emptyTitle: 'هنوز چکی ثبت نشده است', emptyBody: 'پس از ثبت اولین چک، اطلاعات آن در اینجا نمایش داده می‌شود.',
  emptyFiltered: 'چکی با این فیلترها پیدا نشد', emptyFilteredBody: 'فیلترها را تغییر دهید یا پاک کنید تا چک‌های بیشتری ببینید.',
  newCheck: 'ثبت چک جدید', backToList: 'بازگشت به چک‌ها', detailTitle: 'چک {type} {number}', summary: 'مشخصات چک',
  lifecycle: 'چرخه عمر', lifecycleHelp: 'چک در کدام مرحله است و چه اقدامی بعد از این ممکن است.', currentStatus: 'وضعیت فعلی',
  relatedInvoice: 'فاکتور مرتبط', noInvoice: 'به فاکتوری مرتبط نیست.', settled: 'تسویه‌شده با این چک',
  impactHelp: 'همه اسنادی که این چک صادر کرده است. برگشت، سند اصلی را نگه می‌دارد و اثر آن را خنثی می‌کند.',
  reversal: 'برگشت', entry: 'سند', viewEntry: 'مشاهده سند', availableActions: 'اقدامات ممکن', noActions: 'در این وضعیت اقدام دیگری ممکن نیست.',
  otherReports: 'گزارش‌های تحلیلی', groupedReports: 'گزارش‌های تفکیکی و جریان نقد', reportTabs: 'نماهای گزارش', applied: 'فیلترهای اعمال‌شده',
  received_path: { received: 'دریافت', in_custody: 'در صندوق', deposited: 'در جریان وصول', cleared: 'وصول شده' },
  issued_path: { draft: 'پیش‌نویس', issued: 'صادر شده', delivered: 'تحویل شده', cleared: 'پاس شده' },
  terminal: 'با وضعیت «{status}» در {date} بسته شد', formTitleReceived: 'ثبت چک دریافتی', formTitleIssued: 'ثبت چک پرداختی',
  formSubtitle: 'چک دریافتی بلافاصله ثبت حسابداری می‌شود (بدهکار اسناد دریافتنی / بستانکار مشتری). چک پرداختی تا زمان صدور پیش‌نویس می‌ماند.',
  sections: { party: 'طرف حساب و مبلغ', instrument: 'مشخصات چک', dates: 'تاریخ‌ها', settlement: 'تسویه و حسابداری', notes: 'شرح' },
  typeChoice: 'نوع چک', saveAndView: 'ثبت چک', exportPdfNote: 'PDF به‌صورت افقی و با سربرگ شرکت تهیه می‌شود.',
  checkClosedEdit: 'این چک ثبت حسابداری شده است. فقط سررسید، شناسه صیادی، سریال، شعبه، نام صاحب حساب و یادداشت قابل تغییر است.',
  upcoming: 'پیش رو', viewRegister: 'مشاهده دفتر چک',
};

export const payrollExtraFa: Widen<typeof payrollExtraEn> = {
  overviewTitle: 'حقوق و دستمزد', overviewSubtitle: 'لیست‌های حقوق، بدهی‌های قانونی و مواردی که نیاز به اقدام دارند.', periodsSubtitle: 'همه ماه‌های حقوق و وضعیت آن‌ها.',
  currentRun: 'لیست حقوق جاری', noRun: 'هنوز لیست حقوقی ایجاد نشده است', noRunBody: 'لیست حقوق یک ماه را ایجاد کنید تا کارکرد را وارد و فیش‌ها را محاسبه کنید.',
  openLiabilities: 'مالیات و بیمه پرداخت‌نشده', noOpenLiabilities: 'همه بدهی‌های مالیات و بیمه حقوق تسویه شده است.',
  recentRuns: 'لیست‌های اخیر', setupChecklist: 'پیش از اولین لیست حقوق',
  checklist: { employees: 'افزودن کارکنان', components: 'بازبینی اجزای حقوق', rules: 'تنظیم جدول مالیات و نرخ‌های بیمه سال', mappings: 'تعیین قواعد ثبت حقوق' },
  steps: { inputs: 'کارکرد', calculate: 'محاسبه', review: 'بازبینی', warnings: 'هشدارها', approve: 'تأیید', post: 'ثبت سند', pay: 'پرداخت', close: 'بستن' },
  stepHelp: {
    inputs: 'کارکرد (روز، اضافه‌کار، مرخصی) و مبالغ ماهانه مانند پاداش و قسط وام را وارد یا از فایل بارگذاری کنید.',
    calculate: 'همه فیش‌ها را بر اساس اجزای حقوق و قواعد جاری محاسبه کنید.',
    review: 'فیش‌ها و جمع‌ها را بررسی کنید. پس از تغییر کارکرد دوباره محاسبه کنید.',
    warnings: 'هشدارهای محاسبه را برطرف یا تأیید کنید.',
    approve: 'تأیید، فیش‌ها را قفل می‌کند. فقط مدیران می‌توانند تأیید کنند.',
    post: 'ثبت سند، هزینه حقوق را ثبت و بدهی‌های مالیات و بیمه را ایجاد می‌کند.',
    pay: 'خالص حقوق را پرداخت کنید (یک سند تجمیعی). برای واریز بانکی، دستور پرداخت را خروجی بگیرید.',
    close: 'بستن، ماه را تکمیل می‌کند. بدهی‌های مالیات و بیمه را نیز تسویه کنید.',
    done: 'این لیست حقوق بسته شده است.',
  },
  tabs: { inputs: 'کارکرد', payslips: 'فیش‌ها', warnings: 'هشدارها', liabilities: 'مالیات و بیمه', payments: 'دستور پرداخت', history: 'سابقه' },
  calcBasis: 'مبنای محاسبه', dayBasis: 'مبنای روز', dayBases: { '30': 'ماه ثابت ۳۰ روزه', actual: 'روزهای واقعی ماه ({days})', fixed: 'مقسوم‌علیه تنظیم‌شده ({days})' },
  divisor: 'تعداد روز', taxTableUsed: 'جدول مالیات', insuranceRuleUsed: 'قاعده بیمه', noneConfigured: 'تعریف نشده', monthlyHoursUsed: 'مقسوم‌علیه ساعتی',
  warnings: {
    no_tax_table: 'برای این ماه جدول مالیات حقوق تعریف نشده؛ مالیات محاسبه نشد.',
    no_insurance_rule: 'برای این ماه قاعده بیمه تأمین اجتماعی تعریف نشده؛ بیمه محاسبه نشد.',
    missing_bank_account: 'برای {employee} شماره شبا یا حساب ثبت نشده است.', zero_net: 'خالص پرداختی {employee} صفر است.',
    default_inputs: '{employee}: کارکردی وارد نشده؛ ماه کامل در نظر گرفته شد.',
  },
  warningProblems: { missing_bank_account: 'شماره شبا یا حساب ثبت نشده است', zero_net: 'خالص پرداختی صفر است', default_inputs: 'کارکردی وارد نشده — ماه کامل در نظر گرفته شد' },
  noWarnings: 'هشداری وجود ندارد. محاسبه مورد نیازمند بررسی پیدا نکرد.', warningCount: '{count} هشدار',
  importTitle: 'بارگذاری کارکرد', importHelp: 'فایل CSV یا Excel با یک سطر برای هر کارمند و روز بارگذاری کنید. ابتدا کل فایل بررسی می‌شود و تا همه سطرها معتبر نباشند چیزی ثبت نمی‌شود.',
  importColumns: 'ستون‌ها: کد پرسنلی، تاریخ (۱۴۰۵/۰۷/۰۱)، ساعت کار، اضافه کار، غیبت، مرخصی، مرخصی بدون حقوق، ماموریت، تعطیل کاری. غیبت، مرخصی و ماموریت کسری از روز (۰ تا ۱) هستند.',
  preview: 'پیش‌نمایش', validate: 'بررسی فایل', confirmImport: 'ثبت {count} سطر', importDone: 'کارکرد ثبت شد: {rows} سطر برای {employees} کارمند.',
  importSummary: { total_rows: 'کل سطرها', valid_rows: 'سطرهای معتبر', invalid_rows: 'سطرهای نامعتبر', warnings: 'هشدارها', imported_rows: 'سطرهای ثبت‌شده', employees: 'کارکنان' },
  importErrorsTitle: 'خطاها — فایل را اصلاح و دوباره بارگذاری کنید', importWarningsTitle: 'هشدارها', downloadErrors: 'دریافت گزارش خطا',
  row: 'سطر', column: 'ستون', value: 'مقدار', problem: 'مشکل', importHistory: 'بارگذاری‌ها', days: 'روزها', workingHours: 'ساعت کار',
  importIssues: {
    required: 'مقدار وارد نشده', unknown_employee: 'کد پرسنلی ناشناخته', employee_not_in_period: 'در این ماه شاغل نبوده',
    invalid_date: 'تاریخ نامعتبر', date_outside_period: 'تاریخ خارج از ماه حقوق', date_outside_employment: 'تاریخ خارج از دوره اشتغال',
    invalid_number: 'عدد نامعتبر', negative: 'مقدار منفی', out_of_range: 'خارج از محدوده (ساعت ≤ ۲۴، روز ≤ ۱)', hours_exceed_day: 'بیش از ۲۴ ساعت در روز',
    day_overbooked: 'غیبت و مرخصی بیش از یک روز', duplicate_row: 'کارمند و تاریخ تکراری (اولین بار در سطر {value})',
    payroll_locked: 'لیست حقوق تأیید شده؛ برای بارگذاری آن را بازگشایی کنید', absence_with_hours: 'غایب اما ساعت کار ثبت شده',
    employee_missing: 'سطری ندارد؛ کارکرد پیش‌فرض حفظ می‌شود', no_working_hours_column: 'ستون ساعت کار وجود ندارد؛ فقط غیبت، مرخصی و اضافه‌کار ثبت می‌شود',
  },
  liabilitiesTitle: 'بدهی‌های مالیات و بیمه', liabilitiesHelp: 'هنگام ثبت سند حقوق ایجاد می‌شوند. پرداخت از طریق خزانه انجام می‌شود و سند بدهکار بدهی / بستانکار بانک صادر می‌کند.',
  liabilityKinds: { employee_tax: 'مالیات حقوق کسرشده', employee_insurance: 'بیمه سهم کارمند', employer_insurance: 'بیمه سهم کارفرما', other: 'سایر کسور حقوق' },
  liabilityStatus: { open: 'پرداخت‌نشده', partial: 'بخشی پرداخت‌شده', paid: 'پرداخت‌شده' }, paid: 'پرداخت‌شده', outstanding: 'مانده', payLiability: 'ثبت پرداخت',
  payLiabilityTitle: 'پرداخت {kind} — {period}', payFrom: 'پرداخت از', liabilityPaid: 'پرداخت در خزانه ثبت شد.', noLiabilities: 'بدهی‌ها پس از ثبت سند حقوق ایجاد می‌شوند.',
  paymentsHelp: 'برای هر کارمند یک دستور واریز برای بانک. خروجی گرفتن سندی ثبت نمی‌کند؛ پرداخت حقوق همچنان یک سند تجمیعی است.',
  paymentsLocked: 'دستور پرداخت پس از تأیید لیست حقوق در دسترس است.', layout: 'قالب',
  layouts: { generic: 'فایل بارگذاری بانک', report: 'گزارش با جمع' }, exportPayments: 'خروجی دستور پرداخت',
  issues: { missing_bank_account: 'بدون حساب بانکی', invalid_iban: 'شبای نامعتبر', missing_national_id: 'بدون کد ملی' }, issueCount: '{count} سطر نیاز به بررسی دارد',
  settingsSubtitle: 'قواعد قانونی و تنظیمات محاسبه. این مقادیر پیکربندی با تاریخ اعتبار هستند — با تغییر قانون به‌روز کنید.',
  rulesNotice: 'پله‌های مالیات و نرخ‌های بیمه نصب‌شده با سیستم صرفاً نمونه‌اند. پیش از اجرای حقوق، آن‌ها را با ارقام رسمی سال جایگزین کنید.',
  taxTables: 'جداول مالیات حقوق', insuranceRules: 'قواعد بیمه تأمین اجتماعی', newTaxTable: 'جدول مالیات جدید', newInsuranceRule: 'قاعده بیمه جدید',
  editTaxTable: 'ویرایش جدول مالیات', editInsuranceRule: 'ویرایش قاعده بیمه', mode: 'مبالغ', modes: { monthly: 'ماهانه', annual: 'سالانه' },
  modeHelp: 'جدول سالانه روی درآمد مشمول ماهانه × ۱۲ اعمال و مالیات بر ۱۲ تقسیم می‌شود.', exemptionLabel: 'سقف معافیت',
  bracketRate: 'نرخ ٪', employeeRate: 'سهم کارمند ٪', employerRate: 'سهم کارفرما ٪', unemploymentRate: 'بیمه بیکاری ٪',
  ceilingLabel: 'سقف مشمول بیمه (ماهانه، ۰ = بدون سقف)', eligible: 'اجزای مشمول بیمه', eligibleHelp: 'خالی = همه اجزای درآمدی که مشمول بیمه علامت خورده‌اند.',
  dayBasisTitle: 'طول ماه', dayBasisHelp: 'مقسوم‌علیه دستمزد روزانه و محاسبه نسبی. هر محاسبه روش استفاده‌شده را ثبت می‌کند.',
  dayBasisOptions: { '30': 'ماه ثابت ۳۰ روزه', actual: 'روزهای واقعی ماه تقویمی', fixed: 'مقسوم‌علیه قابل تنظیم' },
  variability: 'ثابت / متغیر', variabilities: { fixed: 'ثابت', variable: 'متغیر' }, formula: 'فرمول', formulaHelp: 'عملیات + − × ÷، پرانتز و توابع min()، max()، abs(). متغیرها: {vars}',
  insuranceShare: 'سهم بیمه', insuranceShares: { employee: 'سهم کارمند', employer: 'سهم کارفرما', unemployment: 'بیمه بیکاری' },
  methodsExtra: { formula: 'فرمول', tax_table: 'جدول مالیات (جاری)', insurance_rule: 'قاعده بیمه (جاری)' },
  ruleSourced: 'نرخ از قاعده جاری', componentsSubtitle: 'مزایا، کسورات و سهم کارفرما — نحوه محاسبه هر کدام.',
  employeesSubtitle: 'کارکنان، حقوق و اطلاعات بانکی آن‌ها.', emptyEmployees: 'هنوز کارمندی ثبت نشده است', emptyEmployeesBody: 'کارکنان را اضافه کنید تا در لیست حقوق قرار گیرند.',
  emptyPeriods: 'هنوز لیست حقوقی وجود ندارد', emptyPeriodsBody: 'برای شروع، لیست حقوق یک ماه را ایجاد کنید.', reportsSubtitle: 'جمع حقوق به تفکیک ماه، کارمند، واحد و پروژه.',
  nextAction: 'گام بعد: {action}', employer: 'کارفرما', variable: 'متغیر', fixed: 'ثابت',
};

export const budgetsExtraFa: Widen<typeof budgetsExtraEn> = {
  listSubtitle: 'بودجه‌های نسخه‌دار هر سال مالی.', emptyBudgets: 'هنوز بودجه‌ای ثبت نشده است', emptyBudgetsBody: 'بودجه‌ای ایجاد کنید تا هزینه و درآمد را برنامه‌ریزی و با دفاتر مقایسه کنید.',
  detailSubtitle: 'برنامه، عملکرد و پیش‌بینی بر اساس اسناد ثبت‌شده.', forecastMethod: 'روش پیش‌بینی',
  forecastMethods: { straight_line: 'خطی (نرخ جاری)', historical_average: 'میانگین ماه‌های اخیر', previous_year: 'ماه‌های مشابه سال قبل', manual: 'پیش‌بینی مدیر' },
  forecastMethodHelp: {
    straight_line: 'عملکرد تا امروز با نرخ ماهانه از ابتدای سال تا پایان سال امتداد می‌یابد.', historical_average: 'عملکرد تا امروز به‌علاوه میانگین سه ماه کامل اخیر برای هر ماه باقی‌مانده.',
    previous_year: 'عملکرد تا امروز به‌علاوه عملکرد سال قبل در ماه‌های باقی‌مانده.', manual: 'رقمی که مدیر تعیین کرده؛ سطرهای بدون آن از روش خطی استفاده می‌کنند.',
  },
  forecastBasis: {
    run_rate: 'نرخ جاری {n} ماه', trailing_average: 'میانگین {n} ماه', prior_year_same_periods: 'ماه‌های سال قبل', manager_override: 'تعیین‌شده توسط مدیر',
    fallback_straight_line: 'داده کافی نیست — روش خطی', fallback_no_prior_year: 'سال قبل وجود ندارد — روش خطی', fallback_no_manual: 'پیش‌بینی مدیر ندارد — روش خطی',
    actual: 'عملکرد',
  },
  coverage: { high: 'داده کافی', medium: 'داده محدود', low: 'داده اندک', manual: 'دستی' }, coverageHelp: 'پیش‌بینی بر چند ماه داده استوار است. این یک محاسبه ریاضی است، نه پیش‌بینی آماری.',
  setForecast: 'تعیین پیش‌بینی', setForecastTitle: 'پیش‌بینی مدیر — {line}', forecastAmount: 'پیش‌بینی کل سال', forecastReason: 'دلیل',
  forecastHistory: 'سابقه پیش‌بینی', forecastSaved: 'پیش‌بینی ذخیره شد.', noForecastHistory: 'هنوز پیش‌بینی مدیر ثبت نشده است.',
  kpi: { budget: 'بودجه (دوره)', actual: 'عملکرد', remaining: 'مانده (سالانه)', variance: 'انحراف', utilization: 'درصد مصرف', forecast: 'پیش‌بینی (سال)' },
  comparePeriod: 'بازه مقایسه', favourable: 'مطلوب', unfavourable: 'نامطلوب', monthly: 'بودجه و عملکرد ماهانه', linesTitle: 'سطرها',
  controlSubtitle: 'قواعدی که برای ثبت بیش از بودجه هشدار می‌دهند، تأیید می‌خواهند یا جلوی ثبت را می‌گیرند، و تصمیم‌های گرفته‌شده.',
  eventTitle: 'رویداد کنترل بودجه', event: {
    budget: 'بودجه', account: 'حساب', costCenter: 'مرکز هزینه', transaction: 'تراکنش', limit: 'سقف', originalBudget: 'بودجه',
    actual: 'عملکرد پس از ثبت', requested: 'مبلغ درخواستی', exceeded: 'مازاد بر سقف', user: 'کاربر', role: 'نقش', reason: 'دلیل',
    decision: 'تصمیم', timestamp: 'زمان', policy: 'سیاست', basis: 'مبنای مقایسه', threshold: 'آستانه', notPosted: 'ثبت نشد (مسدود)',
  },
  outcomesExtra: { blocked: 'مسدود' }, decisions: { warned: 'مجاز با هشدار', confirmed: 'تأیید کاربر', overridden: 'عبور از سقف تأیید شد', blocked: 'مسدود شد' },
  noEvents: 'هنوز رویدادی ثبت نشده است', noEventsBody: 'هشدارها، تأییدها و عبور از سقف هنگام ثبت بیش از بودجه در اینجا نمایش داده می‌شوند.',
  noRules: 'قاعده کنترلی تعریف نشده', noRulesBody: 'بدون قاعده، بودجه‌های فعال فقط هشدار می‌دهند. برای سقف نرم یا سخت قاعده اضافه کنید.',
  reportsSubtitle: 'مقایسه بودجه با اسناد ثبت‌شده به تفکیک سطر، حساب، مرکز هزینه یا ماه.', dashboardSubtitle: 'عملکرد بودجه از ابتدای سال تاکنون.',
  formSubtitle: 'مبالغ را برای هر حساب و مرکز هزینه برنامه‌ریزی و بین ماه‌های سال مالی توزیع کنید.',
};

export const errorsExtraFa: Widen<typeof errorsExtraEn> = {
  rule_period_overlap: 'قاعده فعال دیگری در بخشی از این دوره معتبر است. ابتدا آن را پایان دهید یا تاریخ‌ها را تغییر دهید.',
  formula_error: 'فرمول جزء «{component}» قابل محاسبه نیست (کارمند {employee}).',
  liabilities_paid: 'مالیات یا بیمه این لیست حقوق پرداخت شده است. ابتدا آن پرداخت‌ها را در خزانه باطل کنید.',
  liability_paid: 'این بدهی کاملاً پرداخت شده است.', payroll_not_approved: 'برای تهیه دستور پرداخت، ابتدا لیست حقوق را تأیید کنید.',
  import_has_errors: 'فایل {errors} خطا دارد. هیچ داده‌ای ثبت نشد.', unsupported_file: 'فایل CSV یا Excel (.xlsx) بارگذاری کنید.',
  unreadable_file: 'فایل قابل خواندن نیست. آن را به‌صورت CSV (UTF-8) یا ‎.xlsx ذخیره و دوباره امتحان کنید.', empty_file: 'فایل سطر داده ندارد.',
  missing_columns: 'ستون‌های الزامی وجود ندارد: {columns}.', export_too_large: 'تعداد سطرها برای خروجی زیاد است (بیش از {max}). فیلترها را محدودتر کنید.',
  budget_archived: 'برای نسخه بایگانی‌شده بودجه نمی‌توان پیش‌بینی تعیین کرد.',
};

export const validationExtraFa: Widen<typeof validationExtraEn> = {
  invalid_formula: 'فرمول معتبر نیست.', already_used: 'قبلاً جایگزین چک دیگری شده است.',
};

export const auditExtraFa: Widen<typeof auditExtraEn> = {
  export: 'خروجی گرفت', export_payments: 'خروجی دستور پرداخت', import_attendance: 'بارگذاری کارکرد', pay_liability: 'پرداخت بدهی حقوق',
  set_forecast: 'تعیین پیش‌بینی بودجه',
};

export const navExtraFa: Widen<typeof navExtraEn> = { checkDashboard: 'داشبورد چک', checkRegister: 'دفتر چک', payrollOverview: 'نمای کلی حقوق', payrollRuns: 'لیست‌های حقوق', payrollSettings: 'قواعد حقوق', budgetList: 'بودجه‌ها', generalLedger: 'دفتر کل', documents: 'اسناد حسابداری', expand: 'باز کردن {name}', collapse: 'بستن {name}' };
