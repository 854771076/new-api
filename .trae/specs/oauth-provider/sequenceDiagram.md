sequenceDiagram
    participant U as 用户浏览器
    participant FE as 外部前端<br/>app.example.com
    participant BE as 外部后端<br/>api.example.com
    participant NA as new-api<br/>ai.example.com

    U->>FE: 访问外部系统
    FE->>FE: 检查本地 session，无
    FE->>NA: 302 重定向到<br/>https://ai.example.com/oauth/authorize?client_id=...&redirect_uri=https://api.example.com/oauth/callback&state=...
    Note over U,NA: 这是顶层导航，无 CORS<br/>浏览器自动带 ai.example.com 的 cookie
    NA->>NA: 检查 new-api session，已登录
    NA->>U: 展示授权同意页（或跳过）
    U->>NA: 点击同意
    NA->>FE: 302 重定向到<br/>https://api.example.com/oauth/callback?code=xxx&state=xxx
    Note over NA,BE: 回调打到外部后端，不是前端
    FE->>BE: 转发 code 给后端
    BE->>NA: POST /oauth/token<br/>{code, client_id, client_secret, code_verifier}
    Note over BE,NA: 服务端到服务端，无跨域
    NA-->>BE: {access_token, refresh_token, id_token}
    BE->>NA: GET /oauth/userinfo<br/>Authorization: Bearer access_token
    NA-->>BE: {sub, username, email}
    BE->>BE: 建立外部系统自己的 session
    BE-->>FE: Set-Cookie: external_session=xxx
    FE-->>U: 登录成功，展示业务页面