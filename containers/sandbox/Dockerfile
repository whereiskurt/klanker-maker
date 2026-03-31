FROM amazonlinux:2023

# ============================================================
# 1. System packages
# ============================================================
RUN dnf install -y --allowerasing \
      shadow-utils \
      tar \
      gzip \
      unzip \
      jq \
      git \
      ca-certificates \
      python3 \
      procps-ng \
      findutils \
      which \
      less \
      vim-minimal \
      hostname \
      iputils \
      bind-utils \
      curl \
    && dnf clean all \
    && rm -rf /var/cache/dnf

# ============================================================
# 2. AWS CLI v2 (official zip method)
# ============================================================
RUN curl -fsSL "https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip" -o /tmp/awscliv2.zip \
    && unzip -q /tmp/awscliv2.zip -d /tmp \
    && /tmp/aws/install \
    && rm -rf /tmp/aws /tmp/awscliv2.zip

# ============================================================
# 2b. Node.js 20 (for claude-code and other JS tools)
# ============================================================
RUN curl -fsSL https://rpm.nodesource.com/setup_20.x | bash - \
    && dnf install -y nodejs \
    && dnf clean all

# ============================================================
# 3. gosu 1.17 (for privilege drop to sandbox user)
# ============================================================
RUN curl -fsSL "https://github.com/tianon/gosu/releases/download/1.17/gosu-amd64" \
      -o /usr/local/bin/gosu \
    && chmod +x /usr/local/bin/gosu \
    && gosu nobody true

# ============================================================
# 4. Sandbox user, workspace, and helper dirs
#    - sandbox user: UID 1000, /home/sandbox, /bin/bash shell
#    - /workspace: writable directory for agent workloads
#    - /opt/km/bin: helper binaries placed here at runtime
# NOTE: Do NOT install SSM agent (km shell uses docker exec / kubectl exec).
#       Do NOT install km-mail-poller or km-upload-artifacts as Go binaries
#       (they are bash functions in entrypoint.sh per locked decision).
# ============================================================
RUN useradd -m -d /home/sandbox -s /bin/bash -u 1000 sandbox \
    && mkdir -p /workspace \
    && chown sandbox:sandbox /workspace \
    && mkdir -p /opt/km/bin

# ============================================================
# 5. Entrypoint
# ============================================================
COPY entrypoint.sh /opt/km/entrypoint.sh
COPY cred-refresh.sh /opt/km/cred-refresh.sh
RUN chmod +x /opt/km/entrypoint.sh /opt/km/cred-refresh.sh

WORKDIR /workspace

ENTRYPOINT ["/opt/km/entrypoint.sh"]
CMD ["/bin/bash"]
