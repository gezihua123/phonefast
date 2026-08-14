# phonefast 工具能力瓶颈测试报告

## 测试环境

| 项 | 值 |
|----|-----|
| 设备 | emulator-5554 (Android API 33, Pixel 6, 1080×2400) |
| phonefast | daemon socket /tmp/phonefast-501.sock |
| 评测来源 | fa_v1_27: 82/116 = 70.7%, 88 个失败 case |
| 测试日期 | 2026-08-12 |

---

## 1. screenshot() — 返回缓存图片

### 测试
- Home 屏截图 → Settings 屏截图 → 比较 md5

### 结果
```
phonefast screenshot:
  Home:     81026 bytes, md5=a56b485a4a377a71f5e4b004b855c1ab
  Settings: 81026 bytes, md5=a56b485a4a377a71f5e4b004b855c1ab  (相同!)
  → 返回缓存图片，不随屏幕变化

adb exec-out screencap:
  Expense: md5=7cad0ae29bce9a6e48ab71519160fc47
  Broccoli: 不同文件大小 → adb 正常
  → adb screencap 正常，phonefast screenshot 异常
```

### 结论
phonefast screenshot() 返回缓存图片，不实时截取。影响所有依赖截图的功能。

---

## 2. ocr() — phonefast OCR vs PaddleOCR 对比

### 测试
- 同一屏幕截图（Broccoli recipe 列表），分别用 phonefast OCR 和 PaddleOCR 识别

### 结果

**phonefast OCR (35 items, conf 0.46-1.0)**
```
text=A,                    conf=0.9982  ← 碎片
text=doT,                   conf=0.9999  ← 碎片
text=vocadoToastwithEgg,   conf=1.0000  ← 碎片，无空格
text=BananaBread,           conf=0.9990  ← 无空格
text=Adan,                  conf=0.5634  ← 乱码
text=Aqu,                   conf=0.4675  ← 乱码
```

**PaddleOCR (20 items, conf 0.98-1.0)**
```
text='Avocado Toast with Egg'          conf=1.0000  ← 完整，有空格
text='A delicious and healthy...'      conf=0.9982  ← 完整句子
text='Banana Bread'                    conf=1.0000  ← 正确空格
text='A classic quick bread...'       conf=0.9908  ← 完整句子
```

### 结论
- phonefast OCR: 文字碎片化（一行拆成 3-4 片），无空格，描述乱码
- PaddleOCR: 文字完整，正确空格，描述可读，置信度全部 >0.98
- **PaddleOCR 全面优于 phonefast OCR**

---

## 3. observe() — a11y 树不暴露自定义 View 子元素

### 测试
- Expense app (com.arduia.expense) 主屏，DB 有 2 条 expense (Coffee/Lunch)
- observe(concise=False, max_elements=200)

### 结果
```
Expense app (32 elements):
  #13 rv_home (RecyclerView) bounds=[0,275][1080,2337]
  ├── #14 CardView "Totals" bounds=[21,296][1059,898]
  └── #24 CardView "Expenses in this Week" bounds=[21,919][1059,1565]
  ← y=1565~2337 (772px) 空白: Coffee/Lunch 不可见!

Broccoli app (55 elements, 对比):
  #21 recycler_view (RecyclerView) bounds=[0,422][1080,2337]
  ├── #22 CardView [clickable] text="Avocado Toast with Egg"  ← 可见!
  ├── #28 CardView [clickable] text="Chicken Soup"             ← 可见!
  └── #34 CardView [clickable] text="Chocolate Cake"            ← 可见!
```

### 结论
- Expense 用自定义 Canvas/draw 渲染条目，不注册到 a11y service
- Broccoli 用标准 CardView+TextView，自动暴露
- Android 框架限制，observe() 无法修复
- **需要 OCR 或视觉能力来补充 a11y 树的盲区**

---

## 4. type() — 与屏幕键盘冲突

### 测试
- 聚焦输入框后 type(text="Spicy Tuna Wraps")

### 结果
```
当前: 字段显示 "Spicy Tuna Wrapsyy"  ← 多了 "yy"（键盘预测输入）
期望: 字段显示 "Spicy Tuna Wraps"
```

### 结论
type() 通过 input text 输入，屏幕键盘预测输入同时打字。需要输入前关闭键盘。

---

## 5. Recipe 测试结果

### 子集评测 (23 cases, fa_recipe_expense_v2)

| Case | 结果 | 步数 | 说明 |
|------|------|------|------|
| ExpenseAddMultiple | ✅ PASS | 25 | |
| ExpenseAddMultipleFromGallery | ❌ FAIL | 20 | 自定义 View |
| ExpenseAddMultipleFromMarkor | ❌ FAIL | 30 | 自定义 View |
| ExpenseAddSingle | ❌ FAIL | 25 | 自定义 View |
| ExpenseDeleteDuplicates | ❌ FAIL | 12 | 自定义 View |
| ExpenseDeleteDuplicates2 | ❌ FAIL | 18 | 自定义 View |
| ExpenseDeleteMultiple | ❌ FAIL | 15 | 自定义 View |
| ExpenseDeleteMultiple2 | ❌ FAIL | 34 | 自定义 View |
| ExpenseDeleteSingle | ❌ FAIL | 9 | 自定义 View |
| NotesRecipeIngredientCount | ✅ PASS | 6 | |
| RecipeAddMultipleRecipes | ✅ PASS | 3 | |
| RecipeAddMultipleRecipesFromImage | ❌ FAIL | 26 | init 用假图片 (26字节) |
| RecipeAddMultipleRecipesFromMarkor | ✅ PASS | 40 | Home 键修复生效 |
| RecipeAddMultipleRecipesFromMarkor2 | ✅ PASS | 30 | Home 键修复生效 |
| RecipeAddSingleRecipe | ✅ PASS | 11 | |
| RecipeDeleteDuplicateRecipes | ❌ FAIL | 12 | 期望1实际2 |
| RecipeDeleteDuplicateRecipes2 | ❌ FAIL | 22 | 期望1实际2 |
| RecipeDeleteDuplicateRecipes3 | ❌ FAIL | 25 | 期望1实际2 |
| RecipeDeleteMultipleRecipes | ✅ PASS | 11 | |
| RecipeDeleteMultipleRecipesWithConstraint | ✅ PASS | 23 | |
| RecipeDeleteMultipleRecipesWithNoise | ✅ PASS | 11 | |
| RecipeDeleteSingleRecipe | ✅ PASS | 15 | |
| RecipeDeleteSingleWithRecipeWithNoise | ✅ PASS | 9 | |

### 修复后重测 (4 失败 cases, fa_recipe_fails)

| Case | 之前 | 修复后 | 步数 | 修复内容 |
|------|------|--------|------|---------|
| RecipeAddMultipleRecipesFromImage | ❌ | ✅ PASS | 30 | init 改为真实 JPEG 图片 (150KB) |
| RecipeDeleteDuplicateRecipes | ❌ | ✅ PASS | 12 | (随机变化) |
| RecipeDeleteDuplicateRecipes2 | ❌ | ❌ FAIL | 22 | 期望1实际2, agent 没删重复 |
| RecipeDeleteDuplicateRecipes3 | ❌ | ❌ FAIL | 25 | 期望1实际2, agent 没删重复 |

---

## 6. 总结论

| 工具 | 状态 | 影响的 case | 修复方案 |
|------|------|------------|---------|
| screenshot() | ❌ 返回缓存 | 间接影响 OCR | 不缓存，每次实时截取 |
| ocr() | ⚠️ 质量低 | 17 case | 用 PaddleOCR 替换 |
| observe() | ❌ 不可见自定义 View | 10 case | Android 框架限制，需 OCR 补充 |
| type() | ❌ 键盘冲突 | 7 case | 输入前关键盘 |
| Home 键 | ✅ 已修复 | +4 Recipe PASS | run_eval.py 每个 case 前按 Home |
| init 数据 | ✅ 已修复 | +1 Recipe PASS | recipes.jpg 改为真实 JPEG |

### 成功率提升路径
```
当前: 82/116 = 70.7%
+ Home 键 (已落地): +4~5 Recipe/Markor
+ init 图片 (已落地): +1 Recipe
+ type 键盘修复: +3~5 Calendar/Markor
+ OCR PaddleOCR: +8~10 Expense/Files (依赖 observe 盲区)
+ 预计天花板: ~100/116 = 86%
```
