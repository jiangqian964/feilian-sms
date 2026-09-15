import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;

/**
 * JDK 双端签名向量生成器（仅使用 JDK 自带类，无第三方依赖）。
 *
 * 作用：定义「先加盐后摘要」的 SHA-1 参考实现：
 *   MessageDigest.getInstance("SHA-1");
 *   md.update(salt.getBytes());            // 注意：先喂 salt（签名密钥）
 *   byte[] bytes = md.digest(msg.getBytes()); // 再喂待签名串，单次摘要
 *   每个字节 (b & 0xFF) + 0x100 的 16 进制取 substring(1) => 两位小写补零 hex
 * 即 sign = lowercaseHex(SHA-1(UTF8(salt) || UTF8(msg)))。
 *
 * 生成/复跑命令（仓库根目录）：
 *   javac -d /tmp/sms-sign-vec testdata/GenSignVectors.java
 *   java -Dfile.encoding=UTF-8 -cp /tmp/sms-sign-vec GenSignVectors
 *
 * 输出固化到 internal/channel/sign/sha1_salt_test.go，Go 端逐字节比对。
 */
public class GenSignVectors {

    private static final int HEX = 16;
    private static final int SHA_FF = 0xFF;
    private static final int SHA_100 = 0x100;

    /** SHA-1 加盐单次摘要参考实现（getBytes 在服务端为 UTF-8）。 */
    public static String encryptSHA(final String msg, String salt) throws Exception {
        StringBuilder sb = new StringBuilder();
        MessageDigest md = MessageDigest.getInstance("SHA-1");
        md.update(salt.getBytes(StandardCharsets.UTF_8));
        byte[] bytes = md.digest(msg.getBytes(StandardCharsets.UTF_8));
        for (int i = 0; i < bytes.length; i++) {
            sb.append(Integer.toString((bytes[i] & SHA_FF) + SHA_100, HEX).substring(1));
        }
        return sb.toString();
    }

    /** 输出一行 pipe 分隔向量。 */
    private static void emit(String name, String salt, String msg) throws Exception {
        System.out.println("CASE=" + name + "|SALT=" + salt + "|MSG=" + msg + "|SIGN=" + encryptSHA(msg, salt));
    }

    public static void main(String[] args) throws Exception {
        // 1) 典型 ASCII 报文：与待签名串同构
        //    timestamp=<毫秒>&nonce=<随机数>&signData=<appSmsId>
        emit("ascii",
                "demo-app-secret",
                "timestamp=1740385174957&nonce=48291&signData=evt-0001");

        // 2) 摘要首字节 < 0x10 补零专测：确定性搜索第一个满足条件的后缀
        String prefix = "timestamp=1740385174957&nonce=48291&signData=pad-";
        int found = -1;
        String foundMsg = null;
        String foundSign = null;
        for (int i = 0; i < 1_000_000; i++) {
            String m = prefix + i;
            MessageDigest md = MessageDigest.getInstance("SHA-1");
            md.update("demo".getBytes(StandardCharsets.UTF_8));
            byte[] d = md.digest(m.getBytes(StandardCharsets.UTF_8));
            if ((d[0] & SHA_FF) < 0x10) {
                found = i;
                foundMsg = m;
                foundSign = encryptSHA(m, "demo");
                break;
            }
        }
        if (found < 0) {
            throw new IllegalStateException("未找到首字节补零用例");
        }
        System.out.println("PADZERO_INDEX=" + found);
        emit("padzero", "demo", foundMsg);
        // 自证：签名首字符必须为 '0'
        if (foundSign.charAt(0) != '0') {
            throw new IllegalStateException("补零用例首字符不是 0: " + foundSign);
        }

        // 3) 中文与特殊字符（UTF-8 编码一致性）
        emit("utf8",
                "示例secret!@#",
                "timestamp=1740385174957&nonce=65535&signData=事件-é-中文");

        // 4) 空 nonce 边界
        emit("empty-nonce",
                "s",
                "timestamp=1&nonce=&signData=x");

        // 5) 6 位 nonce、appSmsId 为 UUID 形态
        emit("six-digit-nonce",
                "app-secret-uuid",
                "timestamp=1740385174000&nonce=100806&signData=e09288e2-a1b3-4b38-84a8-3c673725abcd");
    }
}
