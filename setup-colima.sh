#!/bin/bash

# Colima 快速启动脚本
# 用法: bash setup-colima.sh

set -e

echo "=== Colima 快速启动 ==="

# 1. 检查 colima 是否已安装
if ! command -v colima &> /dev/null; then
    echo "❌ colima 未安装，尝试从本地路径..."
    if [ -f ~/.local/bin/colima ]; then
        export PATH="$HOME/.local/bin:$PATH"
        echo "✅ 已添加 ~/.local/bin 到 PATH"
    else
        echo "❌ colima 不存在，无法继续"
        exit 1
    fi
fi

# 2. 启动 Colima VM
echo "启动 Colima VM..."
colima start || {
    echo "❌ colima start 失败"
    exit 1
}

# 3. 验证连接
echo "✅ Colima 已启动"
colima status

# 4. 配置 Docker 环境变量（可选）
echo ""
echo "=== Docker 环境配置 ==="
eval $(colima docker context use colima)
docker version

echo ""
echo "✅ 全部完成！现在可以使用 Docker 了"
echo ""
echo "常用命令:"
echo "  colima start      # 启动 VM"
echo "  colima stop       # 停止 VM"
echo "  colima status     # 查看状态"
echo "  docker ps         # 查看容器"
