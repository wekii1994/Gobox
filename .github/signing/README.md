# Gobox Android Release 固化签名密钥

本目录存放 Gobox 项目默认的固化 Android 发布签名密钥（Release Keystore），用于保障每次 CI/Actions 构建出的 APK 具有完全一致的签名指纹。

### 为什么需要固化签名密钥？
在 Android 系统的安全机制下，若两个安装包的签名证书指纹不一致，覆盖安装时会报 `INSTALL_FAILED_UPDATE_INCOMPATIBLE`（签名不匹配）错误，强制用户必须先卸载旧版本才能安装新版本，导致所有已配置的数据与节点丢失。
通过固化签名密钥，所有后续构建的 Release APK 均具备相同签名，**支持随时直接覆盖安装升级**。

### 密钥参数配置
- **文件路径**：`.github/signing/release.keystore`（同时镜像同步至 `android/app/keystore.jks`）
- **Keystore 格式**：`PKCS12`
- **Keystore 密码**：`android`
- **证书别名 (Alias)**：`gobox`
- **密钥密码**：`android`
- **证书所有人**：`CN=Gobox, OU=Gobox, O=Gobox, C=US`
- **有效期**：30 年（至 2056 年）
- **算法**：RSA 2048-bit + SHA-256

### 自定义签名覆盖（可选）
如果你希望使用自己的私有私钥签名，只需在 GitHub 仓库的 **Settings -> Secrets and variables -> Actions** 中配置以下 Secrets 即可，CI 会自动优先使用 Secrets：
- `KEYSTORE`（base64 编码的 jks/keystore 文件内容）
- `STORE_PASSWORD`
- `KEY_ALIAS`
- `KEY_PASSWORD`
