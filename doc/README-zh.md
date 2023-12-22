# 如何使用自己的插件

[TOC]

## 插件的定义

插件在 ***DataTunerX*** 中有两类：***DataPlugin*** 和 ***ScoringPlugin***。本说明针对 ***DataPlugin*** 介绍制作数据集插件的标准，其中包括**如何定义插件**，**如何组织插件的目录结构**，**如何将插件投入使用**等内容。

***DataPlugin*** 是处理数据集的插件。当用户需要对元数据做进一步处理来满足微调需求时，可以使用自己制作的插件来完成数据处理这一步骤，最后将处理好后的数据集信息通过接口传送给 ***dataset-server*** ， 完成 ***Dataset*** 数据的更新。

## 插件的标准

想要插件被 ***DataTunerX*** 识别并成功输出结果，需要遵循下面的标准步骤。

### 第一步

克隆项目到本地（如果您希望您的插件可以加入进官方仓库，则需要先 *fork* 本项目，通过提交个人 *pull request* 的方法提交您的插件），假设您已经 *fork* 了本项目，进入项目目录：

```shell
~: git clone git@github.com:<Your github id>/data-plugins.git
~: cd data-plugins
```

在 `/data-plugins` 目录下创建自己的目录，目录的名称视为插件的 ***Provider*** 。***Provider*** 定义为插件的供应商，可以是您公司、组织甚至个人的名称，来标识提供本插件的供应者是谁。进入 “***Provider***” 目录再创建第二层目录，第二层目录的名称视为插件的 ***DatasetClass***。***DatasetClass*** 定义为此数据集插件的类型，如插件是以 *Argo Workflow* 的方式运行，则 ***DatasetClass*** 指明 `Argo Workflow`：

```shell
/data-plugins: mkdir sample-provider && cd sample-provider
/sample-provider: mkdir sample-dataset-class && cd sample-dataset-class
```

```shell
/data-plugins: tree -L 3
.
├── LICENSE
├── README.md
└── sample-provider
    └── sample-dataset-class
        └── plugin.yaml

3 directories, 3 files
```

### 第二步

在创建好 “***DatasetClass***” 目录后，您必须在目录下加入一个名为 `plugin.yaml` 的 *k8s* 常规资源对象（或已经在环境安装了相关 *CRD* 的对象）的编排文件模板。`plugin.yaml` 模板遵循 ***Go text/template*** 标准。***Dataset Controller*** 会对 `plugin.yaml` 模板中的变量（如果存在）进行替换然后下发（相关逻辑请见[附加说明](#附加说明)），相关资源对象也就是插件会被创建并启动：

```shell
/sample-dataset-class: ls
plugin.yaml
/sample-dataset-class: cat plugin.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ .Name }}
spec:
  replicas: {{ .Replicas }}
  selector:
    matchLabels:
      app: nginx
  template:
    metadata:
      labels:
        app: nginx
    spec:
      containers:
        - name: nginx
          image: {{ .Image }}
          resources:
            limits:
              memory: "128Mi"
              cpu: "200m"
            requests:
              memory: "64Mi"
              cpu: "100m"
```

#### 附加说明

***DataTunerX*** 支持向插件提供静态固定参数和动态运行时参数，这基于 ***Dataset*** *CRD* 和 ***DataPlugin*** *CRD* 的定义。插件制作者可以在 “***DatasetClass***” 目录下同时新建一个描述本插件的 ***DataPlugin*** *CR* 编排文件 `dataplugin.yaml` 随插件包一同提交，也可以直接部署在自己的安装好 ***DataTunerX*** 的集群上，`dataplugin.yaml` 的格式如下：

```yaml
apiVersion: extension.datatunerx.io/v1beta1
kind: DataPlugin
metadata:
  name: <plugin name>
  namespace: <datatunerx system namespace>
spec:
  provider: <provider code>
  datasetClass: <dataset class code>
  version: <version code>
  parameters: [plugin static fixed parameters in json string format]
```

- Name：指插件的名称
- Namespace：插件会和 ***DataTunerX*** 组件安装在同一个命名空间下面，如 `datatunerx-system`
- Provider：和插件第一层 “***Provider***” 目录同名
- DatasetClass：和插件第二层 “***DatasetClass***” 目录同名，`provider` 和 `datasetClass` 共同指引 ***Dataset Controller*** 应该去哪个路径下寻找 `plugin.yaml` 文件
- Version：描述插件版本
- Parameters：***DataPlugin*** *CR* 中定义的 `parameters` 字段为插件的静态固定参数，需要遵循 *Json string* 格式，如 `'{"param1": "value1", "param2": "2", "param3": "true"}'` 。***Dataset Controller*** 在启动插件时会先用 ***DataPlugin*** *CR* 中的 `parameters` 替换一次 `plugin.yaml` 模板中的值，再读取创建 ***Dataset*** *CR* 时用户写入的动态运行时参数（两种 **Parameters** 使用方法相同 ），覆盖替换一次 `plugin.yaml` 模板中的值，也就是说如果 ***Parameters*** 中有同一个参数字段，则最终生效的是创建 ***Dataset*** *CR* 时写入的动态运行时参数。另外，两种 ***Parameters*** 都可以为空，但需要插件内部逻辑中处理好没有给出任何参数的情况。

### 最后一步

再插件完成数据处理工作后，需要调用**通知接口**告知处理完成的数据集相关信息。接口地址由 ***Dataset Controller*** 生成 `CompleteNotifyUrl` 变量并添加进 ***Parameters*** 中，以便在应用 `plugin.yaml` 时替换相应的变量值。所以此步骤要求插件**务必**实现 `CompleteNotifyUrl` 变量的识别，并在 `plugin.yaml` 中预留好 `CompleteNotifyUrl` 的位置，如：

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ .Name }}
spec:
  replicas: {{ .Replicas }}
  selector:
    matchLabels:
      app: nginx
  template:
    metadata:
      labels:
        app: nginx
    spec:
      containers:
        - name: nginx
          image: {{ .Image }}
          env:
          - name: COMPLETE_NOTIFY_URL
            value: {{ .ComplateNotifyUrl }}
...
```

**通知接口**接受 *HTTP POST* 方法请求，需要传递的 *Request Body* 为：

```json
[
    {
        "name": "Default",  // Dataset subset's name, e.g., Random Sample Subset, Balanced Class Subset, Time Window Subset, Feature Subset, Cross-Validation Subset, Outlier Detection Subset, etc. Default value is "Default" if not specified.
        "splits": {
            "train": {
                "file": "s3://bucket/training_data.csv",  // Training Dataset's address. This can be an S3 protocol address, indicating the location of the training data file on an S3 bucket.
            },
            "test": {
                "file": "s3://bucket/testing_data.csv",  // Testing Dataset's address. Similar to the training data address, this can be an S3 protocol address.
            },
            "validate": {
                "file": "s3://bucket/validation_data.csv",  // Validation Dataset's address. Similar to the training data address, this can be an S3 protocol address.
            }
        }
    }
]
```
{
  "score": 100,
  "metrics": ["ROUGE"],
  "details": {"rouge1", "rouge2", "rougeL", "rouge"}
}

数据集包含所有可用的数据，可以根据特定规则或目的选择一部分数据当作子集，子集进一步拆分为训练集、测试集、验证集。插件在处理好这样的数据结构后，将数据集信息传递到**通知接口**，更新 ***Dataset*** 信息，***Dataset*** 为可用状态。

至此，您已完成制作和使用 ***DataTunerX*** 数据集插件 ***DataPlugin*** 的所有步骤。