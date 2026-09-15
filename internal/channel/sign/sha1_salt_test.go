package sign

import "testing"

// JDK 向量来源：testdata/GenSignVectors.java（仅使用 JDK 自带类的
// SHA-1 加盐参考实现），生成命令见该文件头注释；由真实 JDK 17 运行产出，
// 固化于 /tmp/sign_vectors.txt（2026-09-15）。
var jdkVectors = []struct {
	name string
	salt string
	msg  string
	sign string
}{
	{
		name: "ascii",
		salt: "demo-app-secret",
		msg:  "timestamp=1740385174957&nonce=48291&signData=evt-0001",
		sign: "a4d89e4c1019d943b1738ac4613084be231693bf",
	},
	{
		name: "padzero",
		salt: "demo",
		msg:  "timestamp=1740385174957&nonce=48291&signData=pad-21",
		sign: "08c402718259554a338c3d4e62543a61298f0cc4",
	},
	{
		name: "utf8",
		salt: "示例secret!@#",
		msg:  "timestamp=1740385174957&nonce=65535&signData=事件-é-中文",
		sign: "d3ec3f8f6c0156c9ba791a0b88142ca9391914c2",
	},
	{
		name: "empty-nonce",
		salt: "s",
		msg:  "timestamp=1&nonce=&signData=x",
		sign: "cd8bff8d6b602028cc1d6bf4ab368587399ca21d",
	},
	{
		name: "six-digit-nonce",
		salt: "app-secret-uuid",
		msg:  "timestamp=1740385174000&nonce=100806&signData=e09288e2-a1b3-4b38-84a8-3c673725abcd",
		sign: "dd3a902b99d005ca38686359ad2662aeffe89d76",
	},
}

func TestSHA1SaltMatchesJDK(t *testing.T) {
	s, err := New(StrategySHA1Salt, "")
	if err != nil {
		t.Fatalf("创建 sha1_salt 失败: %v", err)
	}
	for _, v := range jdkVectors {
		t.Run(v.name, func(t *testing.T) {
			got, err := s.Sign(v.salt, v.msg)
			if err != nil {
				t.Fatal(err)
			}
			if got != v.sign {
				t.Fatalf("与 JDK 向量不一致\n got: %s\nwant: %s", got, v.sign)
			}
			if len(got) != 40 {
				t.Fatalf("SHA-1 hex 长度应为 40，实际 %d", len(got))
			}
		})
	}
}

func TestSHA1SaltLeadingZeroByte(t *testing.T) {
	v := jdkVectors[1] // padzero
	if v.sign[0] != '0' {
		t.Fatalf("测试向量选择错误：签名应以 0 开头，实际 %s", v.sign)
	}
	s, _ := New(StrategySHA1Salt, "")
	got, err := s.Sign(v.salt, v.msg)
	if err != nil {
		t.Fatal(err)
	}
	// 补零必须真实存在：未补零会得到 "6ecd..."（39 位），
	// 这里锁定输出 40 位且首位为 '0'（对应摘要首字节 0x06）。
	if len(got) != 40 || got[0] != '0' {
		t.Fatalf("首字节 <0x10 的两位补零断言失败: %s", got)
	}
}

func TestNoneSigner(t *testing.T) {
	s, err := New(StrategyNone, "")
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Sign("any-secret", "any-raw")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("none 策略签名必须为空，实际 %q", got)
	}
}

func TestUnknownStrategy(t *testing.T) {
	if _, err := New("md5_salt", ""); err == nil {
		t.Fatal("未知签名策略必须报错")
	}
}
