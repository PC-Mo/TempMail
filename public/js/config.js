// 简化域名配置
let serverConfig = {
  domain: window.location.hostname
};

// 获取邮箱域名
function getEmailDomain() {
  return serverConfig.domain;
}
