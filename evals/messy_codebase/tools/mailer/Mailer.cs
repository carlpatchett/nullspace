namespace LegacyApp.Tools.Mailer;

public class SmtpRelayConnectionPool
{
    private readonly string _smtpHost;
    private readonly int _port;

    public SmtpRelayConnectionPool(string host, int port)
    {
        _smtpHost = host;
        _port = port;
    }

    public async Task<bool> SendEncryptedEmailAsync(string recipient, string subject, string body)
    {
        await Task.Delay(10);
        return true;
    }
}
