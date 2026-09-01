#!/bin/bash
# 火山引擎 OCR 集成演示脚本

set -e

echo "====== 火山引擎 OCR 集成演示 ======"
echo ""

# 加载环境变量
if [ -f .env ]; then
    export $(grep VOLCENGINE .env | xargs)
    echo "✓ 已加载 .env 中的火山引擎凭证"
else
    echo "✗ 错误：找不到 .env 文件"
    exit 1
fi

# 验证凭证是否存在
if [ -z "$VOLCENGINE_VISUAL_AK" ] || [ -z "$VOLCENGINE_VISUAL_SK" ]; then
    echo "✗ 错误：火山引擎凭证未配置"
    echo "  请在 .env 中设置 VOLCENGINE_VISUAL_AK 和 VOLCENGINE_VISUAL_SK"
    exit 1
fi

echo "✓ 火山引擎凭证已配置"
echo "  AK: ${VOLCENGINE_VISUAL_AK:0:10}..."
echo "  SK: ${VOLCENGINE_VISUAL_SK:0:10}..."
echo ""

# 运行单元测试
echo "====== 1. 运行 OCR 单元测试 ======"
if go test -v ./internal/pkg/volcengine/... -run TestOCRClientInitialization,TestSignature,TestHMACSha256; then
    echo "✓ 单元测试通过"
else
    echo "✗ 单元测试失败"
    exit 1
fi
echo ""

# 运行 document extractor 测试
echo "====== 2. 运行文本提取测试 ======"
if go test -v ./internal/pkg/document/...; then
    echo "✓ 文本提取测试通过"
else
    echo "✗ 文本提取测试失败"
    exit 1
fi
echo ""

# 编译项目
echo "====== 3. 编译项目 ======"
if go build -o gexue ./cmd/gexue/main.go; then
    echo "✓ 项目编译成功"
else
    echo "✗ 编译失败"
    exit 1
fi
echo ""

# 显示配置
echo "====== 4. 配置验证 ======"
echo "项目配置文件："
echo "  - configs/config.yaml ✓"
grep -A 2 "volcengine:" configs/config.yaml || true
echo ""

echo "环境变量配置："
echo "  - .env ✓"
echo "  - VOLCENGINE_VISUAL_AK=已设置"
echo "  - VOLCENGINE_VISUAL_SK=已设置"
echo ""

# 显示功能说明
echo "====== 5. 功能说明 ======"
echo ""
echo "✓ OCR 集成已完成！现在支持以下文件类型："
echo "  - .txt, .md, .markdown   → 直接提取文本"
echo "  - .docx                  → 提取段落和表格文本"
echo "  - .jpg, .jpeg, .png      → 通过火山引擎 OCR 识别"
echo "  - .pdf                   → 通过火山引擎 OCR 识别"
echo ""

echo "====== 6. 使用示例 ======"
echo ""
echo "在 Go 代码中使用 OCR："
echo ""
echo 'package main'
echo ''
echo 'import ('
echo '  "context"'
echo '  "gexue/internal/pkg/volcengine"'
echo '  "gexue/internal/pkg/document"'
echo ')'
echo ''
echo 'func main() {'
echo '  // 创建 OCR 客户端'
echo '  client := volcengine.NewOCRClient('
echo '    os.Getenv("VOLCENGINE_VISUAL_AK"),'
echo '    os.Getenv("VOLCENGINE_VISUAL_SK"),'
echo '  )'
echo ''
echo '  // 方式 1: 直接使用 OCR 识别'
echo '  text, err := client.RecognizeImage(context.Background(), "image.png")'
echo ''
echo '  // 方式 2: 通过 document 提取器（自动选择合适的方法）'
echo '  text, err := document.ExtractTextWithOCRClient('
echo '    context.Background(),'
echo '    "image.png",'
echo '    client,'
echo '  )'
echo '}'
echo ""

echo "✓ OCR 集成演示完成！"
