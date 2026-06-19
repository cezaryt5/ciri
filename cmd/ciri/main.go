package ciri

import (
	"fmt"
	"os"
)

func main() {
	// 1. 打开文件
	file, err := os.Open("./main.go")
	if err != nil {
		fmt.Println("open file failed, err:", err)
		return
	}

	// 2. 关闭文件
	file.Close()
}
