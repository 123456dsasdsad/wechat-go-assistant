# 可选CPU转写引擎

Go Worker通过独立可执行入口调用Python引擎，主助手业务与微信协议仍为Go。依赖[faster-whisper](https://github.com/SYSTRAN/faster-whisper)、[pilk](https://github.com/foyoux/pilk)及固定版本模型，不使用转写API。

在独立虚拟环境安装`faster-whisper==1.2.1`、`pilk==0.2.4`，安装后保存`pip freeze`。pilk编译需要Python开发头文件和C编译器；可将本系统匹配的开发包解压到私有目录，通过CFLAGS引用。不要改已有科研虚拟环境。

下载`Systran/faster-whisper-base`模型的`model.bin`、`config.json`、`tokenizer.json`和词表，保存到本脚本旁的`models/base/`；保存实际模型revision。模型文件不进入公共Git仓库。安装下载可通过云服务器的私有代理，执行阶段使用本地文件。

创建可执行包装器，例如：

```sh
#!/bin/sh
exec /home/worker/campus-stack/transcription/.venv/bin/python /home/worker/campus-stack/transcription/transcribe.py "$@"
```

Worker配置：

```json
{"transcription_command":"/home/worker/campus-stack/transcription/transcribe","transcription_timeout_seconds":3600,"max_waiting_tasks":8}
```

调用格式：`transcribe <输入绝对路径> <输出JSON绝对路径>`。输出包含engine、source、language、duration和segments（start、end、text）。失败返回非零状态。Go Worker核验时轴、大小和结构，再生成可阅读TXT供任务使用。支持WAV/MP3/M4A/FLAC/OGG/SILK/AMR/AAC/MP4，具体编码依赖本机PyAV和SILK解码器。微信PCM仅在有效16位采样参数存在时转换。

多语言base模型不能保证所有方言、噪声、专业术语识别正确。原始录音作为依据保留，静音不生成任务指令。CPU固定4线程，每次转写一个输入；同一任务的录音依次转写。
