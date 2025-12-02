# mosdns

功能概述、配置方式、教程等，详见: [wiki](https://irine-sistiana.gitbook.io/mosdns-wiki/)

下载预编译文件、更新日志，详见: [release](https://github.com/IrineSistiana/mosdns/releases)

docker 镜像: [docker hub](https://hub.docker.com/r/irinesistiana/mosdns)
# ospf-mosdns
本项目仅对原版基础上添加了ospf插件用于dns解析后动态将解析结果添加到路由中

### Config Example
``` yaml
  - tag: ospf
    type: ospf
    args:
      #路由有效时间（秒），过期后删除路由，如果有dns缓存需要大于dns缓存时间
      ttl: 21600
      #本机ip 加上局域网子网cidr
      ip: 172.16.2.53/24
      #本机和路由连接的网口
      iface: eth0
      #需要将路由添加到那个routerId中，如果对应本机直接填本机ip
      routerId: 172.16.2.254
      #永久静态路由，mosdns运行期间持续存在的路由,将指定ip数据路由到routerId的路由器中
      persistentRoute:
        #ips:
        #  - "192.168.1.1"
        #  - "192.168.1.0/24"
        files:
          - "/opt/mosdns/dat/geoip_telegram.txt"
          
      init-calls:
        - url: http://172.16.2.53/control/cache_clear
          method: POST
          heads:
            "Authorization": Basic xxxxxxxxxxxxxx

        - url: http://172.16.2.80:8796/restart
          method: GET

      # IpPool 持久化配置（可选）
      # 将动态学习到的 domain -> ip 池周期性保存到本地文件，并在启动时从该文件恢复。
      # 如果不设置或留空则不会进行持久化。
      # ipPoolSaveFile: 保存文件路径（JSON 格式），推荐放在 /var/lib/mosdns/ 或配置目录下
      # ipPoolSaveInterval: 自动保存间隔（秒），设置为 0 表示不启动自动保存
      ipPoolSaveFile: "/var/lib/mosdns/ospf_ip_pool.json"
      ipPoolSaveInterval: 60

      # 可选：向远程路由器推送临时主机路由（方案A，用于加速路由可达性）
      # routerType: 指定路由器类型，目前支持 `routeros`（MikroTik RouterOS）。
      # routerOS: 当 routerType==routeros 时使用，包含管理连接信息和添加路由时使用的 gateway。
      # 支持两种 SSH 认证方式：私钥（优先）或密码。
      # 例：使用密码认证
      # routerType: routeros
      # routerOS:
      #   host: 192.0.2.254
      #   port: 22
      #   user: admin
      #   password: yourpassword
      #   gateway: 10.0.0.254
      #
      # 例：使用私钥认证（优先）
      # routerType: routeros
      # routerOS:
      #   host: 192.0.2.254
      #   port: 22
      #   user: admin
      #   privateKey: /root/.ssh/id_rsa
      #   privateKeyPassphrase: "" # 若私钥有口令则填写
      #   gateway: 10.0.0.254

  - tag: remote_sequence
    type: sequence
    args:
      - exec: $forward_remote
      #将解析后的dns结果保存到路由表中
      - exec: $ospf
```
说明：
- 当在 args 中指定 `ipPoolSaveFile` 时，插件启动会尝试从该文件加载未过期的 ip 条目并向路由器宣布这些路由。
- `ipPoolSaveInterval` 为自动保存间隔（秒），为 0 或不设置则不自动保存；保存文件采用原子写入（先写 .tmp 再重命名）。
- 保存格式为 JSON 数组，每个元素包含 {"domain","ip","expiration"}，expiration 为 unix nano 时间戳。
- 请确保进程有权写入 `ipPoolSaveFile` 指定的目录（例如 `/var/lib/mosdns/`），否则会在日志中记录错误但不会阻塞启动。
### 注意项
1. 如果RouterId为非本机IP，需要对应的RouterId路由在ospf邻居列表中，我的另一个项目可以简单的添加ospf邻居https://github.com/SvenShi/ospf-neighbor
2. 当前版本仅简单使用版，可能有未知问题





### 致谢
ospf调用相关代码来源@povsister


https://github.com/povsister/v2ray-core
