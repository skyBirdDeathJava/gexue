//go:build ignore

package main

import (
	"fmt"
	"log"
	"os"

	"gexue/internal/pkg/oss"
)

// 演示：阿里云 OSS 集成的完整流程
func main() {
	fmt.Println("====== 阿里云 OSS 配置 - 端到端演示 ======\n")

	// 1. 从环境变量读取凭证
	ak := os.Getenv("OSS_ACCESS_KEY_ID")
	sk := os.Getenv("OSS_ACCESS_KEY_SECRET")
	bucket := os.Getenv("OSS_BUCKET")
	endpoint := os.Getenv("OSS_ENDPOINT")
	accelEndpoint := os.Getenv("OSS_ACCEL_ENDPOINT")

	if ak == "" || sk == "" || bucket == "" || endpoint == "" {
		log.Printf("⚠️  阿里云 OSS 凭证未配置，某些功能将被禁用\n")
		log.Printf("   请在 .env 中设置：\n")
		log.Printf("   - OSS_ACCESS_KEY_ID\n")
		log.Printf("   - OSS_ACCESS_KEY_SECRET\n")
		log.Printf("   - OSS_BUCKET\n")
		log.Printf("   - OSS_ENDPOINT\n")
		log.Printf("   - OSS_ACCEL_ENDPOINT (可选)\n")
		return
	}

	log.Printf("✓ 已加载阿里云 OSS 凭证\n")
	log.Printf("  AccessKey ID: %s...\n", ak[:10])
	log.Printf("  Bucket: %s\n", bucket)
	log.Printf("  Endpoint: %s\n", endpoint)
	if accelEndpoint != "" {
		log.Printf("  Accel Endpoint: %s\n", accelEndpoint)
	}
	log.Printf("\n")

	// 2. 创建 OSS 上传器
	log.Printf("====== 1. 初始化 OSS 上传器 ======\n")
	uploader, err := oss.NewUploader(oss.Config{
		AccessKeyID:     ak,
		AccessKeySecret: sk,
		Bucket:          bucket,
		Endpoint:        endpoint,
		AccelEndpoint:   accelEndpoint,
	})
	if err != nil {
		log.Printf("✗ 初始化失败: %v\n", err)
		log.Printf("  请检查凭证和配置是否正确\n")
		return
	}
	log.Printf("✓ OSS 上传器初始化成功\n\n")

	// 3. 创建测试文件
	log.Printf("====== 2. 创建测试文件 ======\n")
	testFile := "/tmp/oss_demo_test.txt"
	testContent := "这是一个阿里云 OSS 测试文件\n\n内容：\n- 行 1\n- 行 2\n- 行 3"

	if err := os.WriteFile(testFile, []byte(testContent), 0644); err != nil {
		log.Printf("✗ 创建测试文件失败: %v\n", err)
		return
	}
	log.Printf("✓ 创建测试文件: %s\n", testFile)
	log.Printf("  内容大小: %d 字节\n\n", len(testContent))

	// 4. 生成 OSS 对象键
	log.Printf("====== 3. 生成 OSS 对象键 ======\n")
	objectKey := oss.GenerateObjectKey(0, "demo_test.txt")
	log.Printf("✓ 生成的对象键: %s\n\n", objectKey)

	// 5. 上传文件
	log.Printf("====== 4. 上传文件到 OSS ======\n")
	url, err := uploader.Upload(objectKey, testFile)
	if err != nil {
		log.Printf("✗ 上传失败: %v\n", err)
		log.Printf("  可能原因:\n")
		log.Printf("  1. 凭证不正确\n")
		log.Printf("  2. Bucket 不存在\n")
		log.Printf("  3. 权限不足\n")
		log.Printf("  4. 网络连接问题\n")
		return
	}
	log.Printf("✓ 文件上传成功\n")
	log.Printf("  文件 URL: %s\n\n", url)

	// 6. 直接上传内容
	log.Printf("====== 5. 直接上传内容（字节数据） ======\n")
	contentData := []byte("这是直接上传的内容，不从文件读取")
	objectKey2 := oss.GenerateObjectKey(0, "demo_content.txt")

	url2, err := uploader.UploadWithContent(objectKey2, contentData)
	if err != nil {
		log.Printf("✗ 上传失败: %v\n", err)
		return
	}
	log.Printf("✓ 内容上传成功\n")
	log.Printf("  对象键: %s\n", objectKey2)
	log.Printf("  文件 URL: %s\n\n", url2)

	// 7. 显示配置
	log.Printf("====== 6. 配置总结 ======\n")
	log.Printf("✓ OSS 配置已完成\n")
	log.Printf("  - AccessKey ID: 已配置\n")
	log.Printf("  - AccessKey Secret: 已配置\n")
	log.Printf("  - Bucket: %s\n", bucket)
	log.Printf("  - Endpoint: %s\n", endpoint)
	if accelEndpoint != "" {
		log.Printf("  - Accel Endpoint: 已配置\n")
	}
	log.Printf("\n")

	// 8. 使用示例
	log.Printf("====== 7. 代码使用示例 ======\n")
	log.Printf(`
package main

import (
    "gexue/internal/pkg/oss"
    "os"
)

func main() {
    // 创建上传器
    uploader, err := oss.NewUploader(oss.Config{
        AccessKeyID:     os.Getenv("OSS_ACCESS_KEY_ID"),
        AccessKeySecret: os.Getenv("OSS_ACCESS_KEY_SECRET"),
        Bucket:          os.Getenv("OSS_BUCKET"),
        Endpoint:        os.Getenv("OSS_ENDPOINT"),
        AccelEndpoint:   os.Getenv("OSS_ACCEL_ENDPOINT"),
    })
    if err != nil {
        panic(err)
    }

    // 方式 1: 从文件上传
    url, err := uploader.Upload("documents/myfile.pdf", "/local/path/file.pdf")
    if err != nil {
        panic(err)
    }
    println("Uploaded:", url)

    // 方式 2: 从内容上传
    url2, err := uploader.UploadWithContent("documents/content.txt", []byte("hello"))
    if err != nil {
        panic(err)
    }
    println("Uploaded:", url2)

    // 方式 3: 生成对象键
    key := oss.GenerateObjectKey(0, "myfile.txt")
    println("Object Key:", key)
}
`)
	log.Printf("\n")

	// 清理
	os.Remove(testFile)

	log.Printf("====== 完成！✓ ======\n\n")
	log.Printf("相关文档：\n")
	log.Printf("  - OSS_CONFIG.md     : 完整的 OSS 配置文档\n")
	log.Printf("  - .env              : 环境变量配置\n")
	log.Printf("  - internal/pkg/oss  : OSS 上传器实现\n")
	log.Printf("\n")
}
