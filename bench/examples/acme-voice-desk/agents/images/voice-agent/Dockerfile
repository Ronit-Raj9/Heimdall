# Image of Heimdall's reference voice agents (bench/voice): the cascaded
# agent (cascadedagent.py: speech to text, chat with the helpdesk tools, text
# to speech, every call through the gateway) and the Realtime bridge
# (realtimebridge.py: the caller's audio to an OpenAI Realtime event
# endpoint and back), with the protocol helpers. Python standard library
# only, on the bench's helper image. The files are copies of bench/voice;
# keep them in step. Build it from the project root (`bifrost-bench agent
# build` or `suite admit` builds it from the agent config's build section):
#
#   docker build -t heimdall-example/voice-agent:1 agents/images/voice-agent
FROM bench/coding-core-base:1
USER root
COPY cascadedagent.py realtimebridge.py voiceclient.py tonecode.py wsproto.py /opt/voice-agent/
RUN chmod 0755 /opt/voice-agent/cascadedagent.py /opt/voice-agent/realtimebridge.py \
 && python3 -m py_compile /opt/voice-agent/*.py && rm -rf /opt/voice-agent/__pycache__
USER agent
WORKDIR /workspace
